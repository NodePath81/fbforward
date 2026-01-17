package probe

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/metrics"
)

// MeasureRTT performs RTT-only measurement using TCP or UDP probes.
// It collects cfg.Samples at the requested cfg.Rate (samples/sec) and returns
// mean/min/max and jitter (stdev) statistics.
func MeasureRTT(ctx context.Context, cfg RTTConfig) (*RTTStats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultProbePort
	}
	if cfg.Samples <= 0 {
		return nil, errors.New("samples must be > 0")
	}
	if cfg.Rate <= 0 {
		cfg.Rate = 1
	}
	network := strings.ToLower(strings.TrimSpace(cfg.Network))
	if network == "" {
		network = DefaultNetwork
	}
	if network != "tcp" && network != "udp" {
		return nil, errors.New("network must be tcp or udp")
	}

	pingTimeout := cfg.Timeout
	if pingTimeout <= 0 {
		pingTimeout = time.Second
	}
	var pingFn func() (time.Duration, error)
	if network == "tcp" {
		pingFn = func() (time.Duration, error) {
			return metrics.PingTCPWithTimeout(cfg.Target, cfg.Port, pingTimeout)
		}
	} else {
		pingFn = func() (time.Duration, error) {
			return metrics.PingUDPWithTimeout(cfg.Target, cfg.Port, pingTimeout)
		}
	}

	sampler := metrics.NewRTTSampler(cfg.Rate)
	sampler.Start(pingFn)
	defer sampler.Stop()

	samplePeriod := time.Second / time.Duration(cfg.Rate)
	if samplePeriod <= 0 {
		samplePeriod = time.Second
	}
	maxWait := time.Duration(cfg.Samples) * samplePeriod
	minWait := samplePeriod + pingTimeout
	if maxWait < minWait {
		maxWait = minWait
	}
	deadline := time.Now().Add(maxWait)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		stats := sampler.Stats()
		if stats.Samples >= cfg.Samples {
			out := RTTStats{
				Min:     stats.Min,
				Mean:    stats.Mean,
				Max:     stats.Max,
				Jitter:  stats.StdDev,
				Samples: stats.Samples,
			}
			return &out, nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	stats := sampler.Stats()
	if stats.Samples == 0 {
		if rtt, err := pingFn(); err == nil && rtt > 0 {
			return &RTTStats{
				Min:     rtt,
				Mean:    rtt,
				Max:     rtt,
				Jitter:  0,
				Samples: 1,
			}, nil
		}
	}
	out := RTTStats{
		Min:     stats.Min,
		Mean:    stats.Mean,
		Max:     stats.Max,
		Jitter:  stats.StdDev,
		Samples: stats.Samples,
	}
	return &out, nil
}

// RTTMeasurer provides continuous RTT monitoring for a target endpoint.
type RTTMeasurer struct {
	config  RTTConfig
	sampler *metrics.RTTSampler
	pingFn  func() (time.Duration, error)
}

// NewRTTMeasurer creates a new RTT measurer for the provided config.
func NewRTTMeasurer(cfg RTTConfig) *RTTMeasurer {
	return &RTTMeasurer{config: cfg}
}

// Start begins RTT sampling in the background until ctx is canceled.
func (r *RTTMeasurer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.config.Port == 0 {
		r.config.Port = DefaultProbePort
	}
	if r.config.Rate <= 0 {
		r.config.Rate = 1
	}
	network := strings.ToLower(strings.TrimSpace(r.config.Network))
	if network == "" {
		network = DefaultNetwork
	}
	if network != "tcp" && network != "udp" {
		return errors.New("network must be tcp or udp")
	}

	pingTimeout := r.config.Timeout
	if pingTimeout <= 0 {
		pingTimeout = time.Second
	}
	if network == "tcp" {
		r.pingFn = func() (time.Duration, error) {
			return metrics.PingTCPWithTimeout(r.config.Target, r.config.Port, pingTimeout)
		}
	} else {
		r.pingFn = func() (time.Duration, error) {
			return metrics.PingUDPWithTimeout(r.config.Target, r.config.Port, pingTimeout)
		}
	}

	r.sampler = metrics.NewRTTSampler(r.config.Rate)
	r.sampler.Start(r.pingFn)

	go func() {
		<-ctx.Done()
		r.Stop()
	}()

	return nil
}

// GetStats returns the latest RTT statistics snapshot.
func (r *RTTMeasurer) GetStats() *RTTStats {
	if r.sampler == nil {
		return &RTTStats{}
	}
	stats := r.sampler.Stats()
	return &RTTStats{
		Min:     stats.Min,
		Mean:    stats.Mean,
		Max:     stats.Max,
		Jitter:  stats.StdDev,
		Samples: stats.Samples,
	}
}

// Stop stops RTT sampling.
func (r *RTTMeasurer) Stop() {
	if r.sampler != nil {
		r.sampler.Stop()
	}
}
