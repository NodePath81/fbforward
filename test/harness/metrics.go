package harness

import (
	"bufio"
	"net/http"
	"strconv"
	"strings"
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
}

// UpstreamScores holds score values for one upstream.
type UpstreamScores struct {
	TCP     float64
	UDP     float64
	Overall float64
}

// MetricsCollector scrapes /metrics with an auth token.
type MetricsCollector struct {
	AuthToken string
	Endpoint  string
	client    *http.Client
	samples   []MetricsSample
}

// NewMetricsCollector builds a collector for the endpoint.
func NewMetricsCollector(endpoint, token string) *MetricsCollector {
	return &MetricsCollector{
		Endpoint:  endpoint,
		AuthToken: token,
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

// CollectOnce scrapes and stores one sample.
func (m *MetricsCollector) CollectOnce() error {
	req, err := http.NewRequest(http.MethodGet, m.Endpoint, nil)
	if err != nil {
		return err
	}
	if m.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.AuthToken)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	sample := MetricsSample{
		Timestamp:      time.Now(),
		UpstreamScores: make(map[string]UpstreamScores),
	}

	scanner := bufio.NewScanner(resp.Body)
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
		return err
	}
	m.samples = append(m.samples, sample)
	return nil
}

// Samples returns collected metrics.
func (m *MetricsCollector) Samples() []MetricsSample {
	return m.samples
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
