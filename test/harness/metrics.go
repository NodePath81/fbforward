package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MetricsSample is one scrape of fbforward metrics.
type MetricsSample struct {
	Timestamp      time.Time
	PrimaryTag     string
	UpstreamScores map[string]UpstreamScores
	SwitchCount    int
	MemoryBytes    uint64
	Goroutines     int
	MetricsLatency time.Duration
	RPCLatency     time.Duration
}

// UpstreamScores holds score values for one upstream.
type UpstreamScores struct {
	TCP     float64
	UDP     float64
	Overall float64
}

// MetricsCollector scrapes /metrics with an auth token.
type MetricsCollector struct {
	AuthToken    string
	ForwarderPID int
	ForwarderIP  string
	ControlPort  int

	mu      sync.Mutex
	samples []MetricsSample
	errors  []error
}

// NewMetricsCollector builds a collector for the endpoint.
func NewMetricsCollector(forwarderPID int, forwarderIP string, controlPort int, token string) *MetricsCollector {
	if controlPort == 0 {
		controlPort = 8080
	}
	return &MetricsCollector{
		AuthToken:    token,
		ForwarderPID: forwarderPID,
		ForwarderIP:  forwarderIP,
		ControlPort:  controlPort,
	}
}

// CollectOnce scrapes and returns one metrics sample.
func (m *MetricsCollector) CollectOnce() (MetricsSample, error) {
	if m.ForwarderPID == 0 {
		return MetricsSample{}, fmt.Errorf("forwarder pid not set")
	}
	endpoint := fmt.Sprintf("http://%s:%d/metrics", m.ForwarderIP, m.ControlPort)
	args := []string{"curl", "-s", "--max-time", "2", endpoint}
	if m.AuthToken != "" {
		args = []string{"curl", "-s", "--max-time", "2", "-H", "Authorization: Bearer " + m.AuthToken, endpoint}
	}
	start := time.Now()
	output, err := nsenterOutput(m.ForwarderPID, args...)
	latency := time.Since(start)
	if err != nil {
		return MetricsSample{}, err
	}

	sample := MetricsSample{
		Timestamp:      time.Now(),
		UpstreamScores: make(map[string]UpstreamScores),
		MetricsLatency: latency,
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, valStr := parsePromLine(line)
		switch name {
		case "fbforward_active_upstream":
			if labels["upstream"] != "" && valStr == "1" {
				sample.PrimaryTag = labels["upstream"]
			}
		case "fbforward_upstream_score_tcp":
			tag := labels["upstream"]
			v, _ := strconv.ParseFloat(valStr, 64)
			entry := sample.UpstreamScores[tag]
			entry.TCP = v
			sample.UpstreamScores[tag] = entry
		case "fbforward_upstream_score_udp":
			tag := labels["upstream"]
			v, _ := strconv.ParseFloat(valStr, 64)
			entry := sample.UpstreamScores[tag]
			entry.UDP = v
			sample.UpstreamScores[tag] = entry
		case "fbforward_upstream_score_overall":
			tag := labels["upstream"]
			v, _ := strconv.ParseFloat(valStr, 64)
			entry := sample.UpstreamScores[tag]
			entry.Overall = v
			sample.UpstreamScores[tag] = entry
		case "fbforward_memory_alloc_bytes":
			v, _ := strconv.ParseUint(valStr, 10, 64)
			sample.MemoryBytes = v
		case "fbforward_goroutines":
			v, _ := strconv.Atoi(valStr)
			sample.Goroutines = v
		}
	}
	if err := scanner.Err(); err != nil {
		return MetricsSample{}, err
	}
	return sample, nil
}

// CollectRPC fetches the active upstream via /rpc.
func (m *MetricsCollector) CollectRPC() (string, time.Duration, error) {
	if m.ForwarderPID == 0 {
		return "", 0, fmt.Errorf("forwarder pid not set")
	}
	endpoint := fmt.Sprintf("http://%s:%d/rpc", m.ForwarderIP, m.ControlPort)
	payload := "{\"method\":\"GetStatus\",\"params\":{}}"
	args := []string{"curl", "-s", "--max-time", "2", "-X", "POST", "-H", "Content-Type: application/json", "-d", payload, endpoint}
	if m.AuthToken != "" {
		args = []string{"curl", "-s", "--max-time", "2", "-X", "POST", "-H", "Content-Type: application/json", "-H", "Authorization: Bearer " + m.AuthToken, "-d", payload, endpoint}
	}
	start := time.Now()
	output, err := nsenterOutput(m.ForwarderPID, args...)
	latency := time.Since(start)
	if err != nil {
		return "", latency, err
	}
	var resp struct {
		Ok     bool   `json:"ok"`
		Error  string `json:"error"`
		Result struct {
			ActiveUpstream string `json:"active_upstream"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return "", latency, err
	}
	if !resp.Ok {
		if resp.Error == "" {
			resp.Error = "rpc request failed"
		}
		return "", latency, fmt.Errorf("%s", resp.Error)
	}
	return resp.Result.ActiveUpstream, latency, nil
}

// StartPolling begins a 1s polling loop for metrics and RPC status.
func (m *MetricsCollector) StartPolling(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample, err := m.CollectOnce()
			if err != nil {
				m.recordError(err)
				continue
			}
			if tag, latency, err := m.CollectRPC(); err == nil {
				sample.RPCLatency = latency
				if tag != "" {
					sample.PrimaryTag = tag
				}
			} else {
				m.recordError(err)
			}
			m.appendSample(sample)
		}
	}
}

// Samples returns collected metrics.
func (m *MetricsCollector) Samples() []MetricsSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MetricsSample, len(m.samples))
	copy(out, m.samples)
	return out
}

// Latest returns the most recent sample.
func (m *MetricsCollector) Latest() (MetricsSample, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.samples) == 0 {
		return MetricsSample{}, false
	}
	return m.samples[len(m.samples)-1], true
}

// Errors returns recorded polling errors.
func (m *MetricsCollector) Errors() []error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]error, len(m.errors))
	copy(out, m.errors)
	return out
}

// DeriveSwitchCount counts primary changes across samples.
func (m *MetricsCollector) DeriveSwitchCount() int {
	samples := m.Samples()
	if len(samples) < 2 {
		return 0
	}
	count := 0
	prev := samples[0].PrimaryTag
	for i := 1; i < len(samples); i++ {
		curr := samples[i].PrimaryTag
		if curr == "" {
			continue
		}
		if prev != "" && curr != prev {
			count++
		}
		prev = curr
	}
	return count
}

func (m *MetricsCollector) appendSample(sample MetricsSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, sample)
}

func (m *MetricsCollector) recordError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, err)
}

// nsenterOutput runs a command in a namespace and returns stdout.
func nsenterOutput(pid int, args ...string) (string, error) {
	cmdArgs := append([]string{"--preserve-credentials", "--keep-caps", "-t", fmt.Sprint(pid), "-U", "-n", "--"}, args...)
	cmd := exec.Command("nsenter", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nsenter %v failed: %w (%s)", args, err, string(output))
	}
	return string(output), nil
}

// parsePromLine splits a Prometheus text line into name, labels, value.
func parsePromLine(line string) (string, map[string]string, string) {
	labels := make(map[string]string)
	name := line
	val := ""
	if idx := strings.Index(line, " "); idx != -1 {
		name = line[:idx]
		val = strings.TrimSpace(line[idx:])
	}
	if strings.Contains(name, "{") {
		open := strings.Index(name, "{")
		close := strings.Index(name, "}")
		if open != -1 && close != -1 && close > open {
			labelPart := name[open+1 : close]
			name = name[:open]
			parts := strings.Split(labelPart, ",")
			for _, p := range parts {
				if eq := strings.Index(p, "="); eq != -1 {
					key := p[:eq]
					value := strings.Trim(p[eq+1:], "\"")
					labels[key] = value
				}
			}
		}
	}
	return name, labels, val
}
