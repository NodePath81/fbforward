package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Harness orchestrates one scenario run end-to-end.
type Harness struct {
	WorkDir   string
	Scenario  *Scenario
	Topology  *Topology
	Processes *ProcessManager
	Metrics   *MetricsCollector

	AuthToken        string
	ConfigFile       string
	ForwardPort      int
	ControlPort      int
	BaseCIDR         string
	AssertionResults []AssertionResult
	ForwarderStarted time.Time
}

// NewHarness builds a harness for a loaded scenario.
func NewHarness(workDir string, scenario *Scenario) *Harness {
	return &Harness{
		WorkDir:   workDir,
		Scenario:  scenario,
		Processes: NewProcessManager(),
	}
}

// Setup prepares namespaces, shaping, configs, and metrics collector.
func (h *Harness) Setup() error {
	if h.Scenario == nil {
		return fmt.Errorf("nil scenario")
	}
	if err := checkUserNamespaceSupport(); err != nil {
		return err
	}
	baseCIDR := h.scenarioBaseCIDR()
	tags := h.upstreamTags()
	if len(tags) == 0 {
		return fmt.Errorf("no upstreams defined in scenario")
	}
	topo, err := CreateTopology(h.Scenario.Name, baseCIDR, tags)
	if err != nil {
		return err
	}
	h.Topology = topo
	h.BaseCIDR = baseCIDR
	if err := h.applyInitialShaping(); err != nil {
		h.Topology.CleanupAll()
		return err
	}
	configPath, err := h.RenderConfig()
	if err != nil {
		h.Topology.CleanupAll()
		return err
	}
	h.ConfigFile = configPath
	h.Metrics = NewMetricsCollector(h.Topology.ForwarderNS.ShellPID, h.Topology.ForwarderNS.Subnet.Endpoint, h.ControlPort, h.AuthToken)
	return nil
}

// RenderConfig merges overrides into base config and writes to the workdir.
func (h *Harness) RenderConfig() (string, error) {
	if h.Topology == nil {
		return "", fmt.Errorf("topology not set")
	}
	upstreamCount := len(h.Topology.UpstreamNS)
	basePath := filepath.Join("test", "testdata", fmt.Sprintf("fbforward-%dup.yaml", upstreamCount))
	data, err := os.ReadFile(basePath)
	if err != nil {
		return "", err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	if h.Scenario != nil && h.Scenario.Overrides != nil {
		deepMerge(cfg, h.Scenario.Overrides)
	}

	upstreams, ok := cfg["upstreams"].([]any)
	if !ok {
		return "", fmt.Errorf("upstreams not found in base config")
	}
	for i := 0; i < len(upstreams) && i < len(h.Topology.UpstreamNS); i++ {
		upMap, ok := toStringMap(upstreams[i])
		if !ok {
			continue
		}
		endpoint := h.Topology.UpstreamNS[i].Subnet.Endpoint
		dest, _ := toStringMap(upMap["destination"])
		if dest == nil {
			dest = make(map[string]any)
		}
		dest["host"] = endpoint
		upMap["destination"] = dest
		meas, _ := toStringMap(upMap["measurement"])
		if meas == nil {
			meas = make(map[string]any)
		}
		meas["host"] = endpoint
		if _, ok := meas["port"]; !ok {
			meas["port"] = 9876
		}
		upMap["measurement"] = meas
		upstreams[i] = upMap
	}
	cfg["upstreams"] = upstreams

	if control, ok := toStringMap(cfg["control"]); ok {
		if token, ok := control["auth_token"].(string); ok {
			h.AuthToken = token
		}
		if port, ok := toInt(control["bind_port"]); ok {
			h.ControlPort = port
		}
	}
	if forwarding, ok := toStringMap(cfg["forwarding"]); ok {
		if listeners, ok := forwarding["listeners"].([]any); ok && len(listeners) > 0 {
			if listener, ok := toStringMap(listeners[0]); ok {
				if port, ok := toInt(listener["bind_port"]); ok {
					h.ForwardPort = port
				}
			}
		}
	}
	if h.ForwardPort == 0 {
		h.ForwardPort = 1080
	}
	if h.ControlPort == 0 {
		h.ControlPort = 8080
	}

	outPath := h.ConfigPath("fbforward.yaml")
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

// Start launches fbmeasure, fbforward, and optional iperf3 processes.
func (h *Harness) Start() error {
	if h.Topology == nil {
		return fmt.Errorf("topology not set")
	}
	if h.ConfigFile == "" {
		path, err := h.RenderConfig()
		if err != nil {
			return err
		}
		h.ConfigFile = path
	}
	logDir := h.ConfigPath("logs")

	for _, ns := range h.Topology.UpstreamNS {
		name := fmt.Sprintf("fbmeasure-%s", ns.Tag)
		if err := h.Processes.Start(name, ns.ShellPID, "fbmeasure", []string{"--port", "9876"}, logDir); err != nil {
			return err
		}
	}
	for _, ns := range h.Topology.UpstreamNS {
		if err := waitForTCP(h.Topology.Hub.ShellPID, ns.Subnet.Endpoint, 9876, 5*time.Second); err != nil {
			return err
		}
	}

	h.ForwarderStarted = time.Now()
	if err := h.Processes.Start("fbforward", h.Topology.ForwarderNS.ShellPID, "fbforward", []string{"--config", h.ConfigFile}, logDir); err != nil {
		return err
	}
	if err := h.waitForMetrics(10 * time.Second); err != nil {
		return err
	}

	if strings.EqualFold(h.Scenario.Name, "stability") {
		for _, ns := range h.Topology.UpstreamNS {
			name := fmt.Sprintf("iperf3-server-%s", ns.Tag)
			if err := StartIperf3Server(h.Processes, name, ns.ShellPID, h.ForwardPort, logDir); err != nil {
				return err
			}
		}
		for _, ns := range h.Topology.UpstreamNS {
			if err := waitForTCP(h.Topology.Hub.ShellPID, ns.Subnet.Endpoint, h.ForwardPort, 5*time.Second); err != nil {
				return err
			}
		}
		duration := int(h.scenarioDuration().Seconds())
		if duration <= 0 {
			duration = 60
		}
		duration += 30
		if err := StartIperf3Clients(h.Processes, "iperf3-client", h.Topology.TrafficSourceNS.ShellPID, h.Topology.ForwarderNS.Subnet.Endpoint, h.ForwardPort, duration, 10, logDir); err != nil {
			return err
		}
	}
	return nil
}

// Run executes scenario timeline actions.
func (h *Harness) Run() error {
	if h.Scenario == nil {
		return fmt.Errorf("nil scenario")
	}
	if h.Metrics == nil {
		return fmt.Errorf("metrics collector not set")
	}
	interval := time.Duration(h.Scenario.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	events, err := h.parseTimeline()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Metrics.StartPolling(ctx)

	baseline := time.Time{}
	if !h.ForwarderStarted.IsZero() {
		baseline = h.ForwarderStarted.Add(30 * time.Second)
	}

	earlyErr := make(chan error, 1)
	if strings.EqualFold(h.Scenario.Name, "stability") {
		go h.monitorStability(ctx, baseline, earlyErr)
	}

	origin := time.Now()
	for _, ev := range events {
		if ev.Action == "wait_convergence" {
			if err := h.waitForConvergence(interval); err != nil {
				return err
			}
			if len(ev.Assertions) > 0 {
				results := EvaluateAssertions(ev.Assertions, h.Metrics, interval, ev.Offset, baseline)
				h.AssertionResults = append(h.AssertionResults, results...)
			}
			origin = time.Now()
			continue
		}

		target := origin.Add(ev.Offset)
		if err := waitUntil(target, earlyErr); err != nil {
			return err
		}

		if ev.Action != "" {
			if err := h.applyAction(ev); err != nil {
				return err
			}
		}
		if len(ev.Assertions) > 0 {
			results := EvaluateAssertions(ev.Assertions, h.Metrics, interval, ev.Offset, baseline)
			h.AssertionResults = append(h.AssertionResults, results...)
		}
	}
	select {
	case err := <-earlyErr:
		return err
	default:
	}
	return nil
}

// Verify evaluates assertions using collected metrics.
func (h *Harness) Verify() error {
	if len(h.AssertionResults) == 0 {
		return nil
	}
	failed := make([]AssertionResult, 0)
	for _, res := range h.AssertionResults {
		if !res.Passed {
			failed = append(failed, res)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	lines := make([]string, 0, len(failed))
	for _, res := range failed {
		msg := res.Details
		if res.Reason != "" {
			msg = res.Reason + ": " + res.Details
		}
		lines = append(lines, fmt.Sprintf("%s: %s", res.Type, msg))
	}
	return errors.New("assertions failed: " + strings.Join(lines, "; "))
}

// ExportArtifacts writes any logs/outputs. Currently no-op.
func (h *Harness) ExportArtifacts() error {
	return nil
}

// Cleanup tears down namespaces and processes.
func (h *Harness) Cleanup() {
	if h.Processes != nil {
		h.Processes.StopAll()
	}
	if h.Topology != nil {
		h.Topology.CleanupAll()
	}
}

// EnsureWorkDir creates the working directory.
func (h *Harness) EnsureWorkDir() error {
	if h.WorkDir == "" {
		return fmt.Errorf("workdir not set")
	}
	return os.MkdirAll(h.WorkDir, 0o755)
}

// ConfigPath returns path inside workdir.
func (h *Harness) ConfigPath(name string) string {
	return filepath.Join(h.WorkDir, name)
}

func (h *Harness) upstreamTags() []string {
	if h.Scenario == nil || h.Scenario.Shaping == nil {
		return nil
	}
	tags := make([]string, 0, len(h.Scenario.Shaping))
	for tag := range h.Scenario.Shaping {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func (h *Harness) scenarioBaseCIDR() string {
	base := "10.200.0.0/16"
	if h.Scenario == nil {
		return base
	}
	if h.Scenario.Metadata != nil {
		if val := strings.TrimSpace(h.Scenario.Metadata["base_cidr"]); val != "" {
			return val
		}
	}
	if h.Scenario.Raw != nil {
		if val, ok := h.Scenario.Raw["base_cidr"].(string); ok && strings.TrimSpace(val) != "" {
			return val
		}
	}
	return base
}

func (h *Harness) applyInitialShaping() error {
	if h.Topology == nil || h.Scenario == nil {
		return nil
	}
	for tag, rule := range h.Scenario.Shaping {
		ns := h.Topology.UpstreamByTag[tag]
		if ns == nil || ns.VethPair == nil {
			return fmt.Errorf("upstream %s not found in topology", tag)
		}
		if err := ApplyBidirectionalShaping(h.Topology.Hub.ShellPID, ns.VethPair.Hub, rule); err != nil {
			return err
		}
	}
	return nil
}

func (h *Harness) applyAction(ev scheduledEvent) error {
	switch ev.Action {
	case "degrade_upstream", "restore_upstream", "inject_loss", "perturb_bandwidth":
		if ev.NewShaping == nil {
			return fmt.Errorf("missing shaping rule for %s", ev.Action)
		}
		ns := h.Topology.UpstreamByTag[ev.Upstream]
		if ns == nil || ns.VethPair == nil {
			return fmt.Errorf("unknown upstream %s", ev.Upstream)
		}
		return ApplyBidirectionalShaping(h.Topology.Hub.ShellPID, ns.VethPair.Hub, *ev.NewShaping)
	case "":
		return nil
	default:
		return fmt.Errorf("unknown action %s", ev.Action)
	}
}

func (h *Harness) waitForConvergence(interval time.Duration) error {
	cycles := h.Scenario.ConvergenceCycles
	if cycles <= 0 {
		cycles = 3
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		ok, primary := DetectConvergence(h.Metrics.Samples(), interval, cycles)
		if ok {
			if primary == "" {
				return fmt.Errorf("convergence primary not detected")
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("convergence timeout")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *Harness) waitForMetrics(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := h.Metrics.CollectOnce(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("metrics endpoint not ready")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *Harness) scenarioDuration() time.Duration {
	max := time.Duration(0)
	for _, ev := range h.Scenario.Timeline {
		d, err := time.ParseDuration(ev.T)
		if err != nil {
			continue
		}
		if d > max {
			max = d
		}
	}
	return max
}

type scheduledEvent struct {
	TimelineEvent
	Offset time.Duration
	Index  int
}

func (h *Harness) parseTimeline() ([]scheduledEvent, error) {
	if h.Scenario == nil {
		return nil, fmt.Errorf("nil scenario")
	}
	events := make([]scheduledEvent, 0, len(h.Scenario.Timeline))
	for i, ev := range h.Scenario.Timeline {
		d, err := time.ParseDuration(ev.T)
		if err != nil {
			return nil, fmt.Errorf("invalid timeline duration %q: %w", ev.T, err)
		}
		events = append(events, scheduledEvent{TimelineEvent: ev, Offset: d, Index: i})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Offset == events[j].Offset {
			return events[i].Index < events[j].Index
		}
		return events[i].Offset < events[j].Offset
	})
	return events, nil
}

func (h *Harness) monitorStability(ctx context.Context, baseline time.Time, errCh chan<- error) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	var baseSample *MetricsSample
	gorIncrease := 0
	lastGor := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			samples := h.Metrics.Samples()
			if len(samples) == 0 {
				continue
			}
			latest := samples[len(samples)-1]
			if baseSample == nil && !baseline.IsZero() {
				for i := range samples {
					if !samples[i].Timestamp.Before(baseline) {
						baseSample = &samples[i]
						lastGor = samples[i].Goroutines
						break
					}
				}
			}
			if baseSample == nil {
				continue
			}
			if int64(latest.MemoryBytes)-int64(baseSample.MemoryBytes) > 100*1024*1024 {
				errCh <- fmt.Errorf("stability: memory delta exceeded 100MB")
				return
			}
			if latest.MetricsLatency > time.Second {
				errCh <- fmt.Errorf("stability: metrics latency > 1s")
				return
			}
			if latest.RPCLatency > time.Second {
				errCh <- fmt.Errorf("stability: rpc latency > 1s")
				return
			}
			if latest.Goroutines > lastGor {
				gorIncrease++
			} else {
				gorIncrease = 0
			}
			lastGor = latest.Goroutines
			if gorIncrease >= 10 {
				errCh <- fmt.Errorf("stability: goroutines increasing for 10 samples")
				return
			}
			if p := h.Processes.Processes["fbforward"]; p != nil {
				select {
				case err := <-p.Done:
					if err != nil {
						errCh <- fmt.Errorf("fbforward exited: %v", err)
					} else {
						errCh <- fmt.Errorf("fbforward exited")
					}
					return
				default:
				}
			}
		}
	}
}

func waitUntil(target time.Time, errCh <-chan error) error {
	for {
		now := time.Now()
		if !now.Before(target) {
			return nil
		}
		wait := target.Sub(now)
		if wait > 200*time.Millisecond {
			wait = 200 * time.Millisecond
		}
		if errCh == nil {
			time.Sleep(wait)
			continue
		}
		select {
		case err := <-errCh:
			return err
		case <-time.After(wait):
		}
	}
}

func waitForTCP(nsPID int, ip string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cmd := fmt.Sprintf("echo > /dev/tcp/%s/%d", ip, port)
	for {
		args := []string{"nsenter", "-t", fmt.Sprint(nsPID), "-U", "-n", "--", "bash", "-c", cmd}
		if err := execCommand(args...); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for tcp %s:%d", ip, port)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func checkUserNamespaceSupport() error {
	if data, err := os.ReadFile("/proc/sys/user/max_user_namespaces"); err == nil {
		val := strings.TrimSpace(string(data))
		if v, err := strconv.Atoi(val); err == nil && v <= 0 {
			return fmt.Errorf("user namespaces disabled: /proc/sys/user/max_user_namespaces=%s", val)
		}
	}
	if data, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone"); err == nil {
		val := strings.TrimSpace(string(data))
		if v, err := strconv.Atoi(val); err == nil && v == 0 {
			return fmt.Errorf("unprivileged user namespaces disabled: /proc/sys/kernel/unprivileged_userns_clone=0")
		}
	}
	return nil
}

func execCommand(args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command")
	}
	cmd := args[0]
	rest := args[1:]
	c := exec.Command(cmd, rest...)
	if output, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("command %v failed: %w (%s)", args, err, string(output))
	}
	return nil
}

func deepMerge(dst, src map[string]any) {
	for key, val := range src {
		if srcMap, ok := toStringMap(val); ok {
			if dstMap, ok := toStringMap(dst[key]); ok {
				deepMerge(dstMap, srcMap)
				dst[key] = dstMap
				continue
			}
		}
		dst[key] = val
	}
}

func toStringMap(val any) (map[string]any, bool) {
	if val == nil {
		return nil, false
	}
	switch v := val.(type) {
	case map[string]any:
		return v, true
	case map[interface{}]interface{}:
		out := make(map[string]any, len(v))
		for k, value := range v {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			out[ks] = value
		}
		return out, true
	default:
		return nil, false
	}
}

func toInt(val any) (int, bool) {
	switch v := val.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		i, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}
