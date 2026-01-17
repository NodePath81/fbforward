package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/metrics"
	"github.com/NodePath81/fbforward/bwprobe/internal/network"
)

// Run executes a network quality test.
func Run(ctx context.Context, cfg Config, progress ProgressFunc) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	networkName := strings.ToLower(strings.TrimSpace(cfg.Network))
	if networkName == "" {
		networkName = "tcp"
	}
	if networkName != "tcp" && networkName != "udp" {
		return Result{}, fmt.Errorf("unsupported network %q (must be tcp or udp)", cfg.Network)
	}
	if cfg.BandwidthBps <= 0 {
		return Result{}, errors.New("bandwidth must be > 0")
	}
	if cfg.Samples <= 0 {
		return Result{}, errors.New("samples must be > 0")
	}
	if cfg.SampleBytes <= 0 {
		return Result{}, errors.New("sample-bytes must be > 0")
	}
	if cfg.ChunkSize <= 0 {
		return Result{}, errors.New("chunk-size must be > 0")
	}
	if cfg.RTTRate <= 0 {
		cfg.RTTRate = 10
	}
	if cfg.Wait < 0 {
		return Result{}, errors.New("wait must be >= 0")
	}
	if cfg.Direction != DirectionUpload && cfg.Direction != DirectionDownload {
		cfg.Direction = DirectionUpload
	}

	var pingFn func() (time.Duration, error)
	if networkName == "tcp" {
		pingFn = func() (time.Duration, error) {
			return metrics.PingTCP(cfg.Target, cfg.Port)
		}
	} else {
		pingFn = func() (time.Duration, error) {
			return metrics.PingUDP(cfg.Target, cfg.Port)
		}
	}

	sampler := metrics.NewRTTSampler(cfg.RTTRate)
	sampler.Start(pingFn)
	initialRTT := waitForRTTSample(sampler, 2*time.Second)
	if initialRTT <= 0 {
		for attempt := 0; attempt < 3; attempt++ {
			if rtt, err := pingFn(); err == nil && rtt > 0 {
				initialRTT = rtt
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	var result Result
	var err error
	if networkName == "tcp" {
		if cfg.Direction == DirectionDownload {
			result, err = runTCPReverse(ctx, cfg, progress, initialRTT)
		} else {
			result, err = runTCP(ctx, cfg, progress, initialRTT)
		}
	} else {
		if cfg.Direction == DirectionDownload {
			result, err = runUDPReverse(ctx, cfg, progress)
		} else {
			limiter := network.New(cfg.BandwidthBps / 8)
			result, err = runUDP(ctx, cfg, progress, limiter)
		}
	}
	sampler.Stop()
	if err != nil {
		return Result{}, err
	}

	rttStats := sampler.Stats()
	result.RTTMean = rttStats.Mean
	result.RTTMin = rttStats.Min
	result.RTTMax = rttStats.Max
	result.RTTStdDev = rttStats.StdDev
	result.RTTSamples = rttStats.Samples

	return result, nil
}

func runTCP(ctx context.Context, cfg Config, progress ProgressFunc, rtt time.Duration) (Result, error) {
	ctrl, err := NewControlClient(cfg.Target, cfg.Port)
	if err != nil {
		return Result{}, err
	}
	defer ctrl.Close()

	// Use RPC session ID if available
	sessionID := ctrl.SessionID()
	var sender network.Sender
	if sessionID != "" {
		sender, err = network.NewTCPSenderWithSession(cfg.Target, cfg.Port, cfg.BandwidthBps, rtt, cfg.ChunkSize, sessionID)
	} else {
		sender, err = network.NewTCPSender(cfg.Target, cfg.Port, cfg.BandwidthBps, rtt, cfg.ChunkSize)
	}
	if err != nil {
		return Result{}, err
	}
	defer sender.Close()

	exec := newSenderExecutor(sender)
	runResult, err := runSampleSeries(ctx, cfg, progress, exec, ctrl, DirectionUpload, reverseStartConfig{})
	if err != nil {
		return Result{}, err
	}

	stats, err := sender.Stats()
	if err != nil {
		return Result{}, err
	}
	if stats.TCP == nil {
		return Result{}, errors.New("missing tcp stats")
	}
	info := *stats.TCP

	trimmed := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.trimmed })
	peak1s := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.peak1s })
	p90 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p90 })
	p80 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p80 })
	achievedBps := trimmed
	if achievedBps <= 0 && runResult.duration.Seconds() > 0 {
		achievedBps = float64(runResult.bytesSent*8) / runResult.duration.Seconds()
	}
	util := achievedBps / cfg.BandwidthBps
	if util < 0 {
		util = 0
	}
	loss := 0.0
	if info.SegmentsSent > 0 {
		loss = float64(info.Retransmits) / float64(info.SegmentsSent)
	}
	payloadSize, err := network.TCPPayloadSize(cfg.ChunkSize)
	if err != nil {
		return Result{}, err
	}
	sendBufBytes := uint64(network.TCPWriteBufferBytes(cfg.BandwidthBps, rtt, payloadSize))

	return Result{
		BytesSent:          runResult.bytesSent,
		Duration:           runResult.duration,
		TargetBps:          cfg.BandwidthBps,
		AchievedBps:        achievedBps,
		Utilization:        util,
		SamplesPlanned:     cfg.Samples,
		SamplesCompleted:   runResult.samplesCompleted,
		Network:            "tcp",
		LossRate:           loss,
		Retransmits:        info.Retransmits,
		SegmentsSent:       info.SegmentsSent,
		TCPSendBufferBytes: sendBufBytes,
		Peak1sBps:          peak1s,
		TrimmedMeanBps:     trimmed,
		P90Bps:             p90,
		P80Bps:             p80,
	}, nil
}

func runUDP(ctx context.Context, cfg Config, progress ProgressFunc, limiter *network.Limiter) (Result, error) {
	ctrl, err := NewControlClient(cfg.Target, cfg.Port)
	if err != nil {
		return Result{}, err
	}
	defer ctrl.Close()

	sessionID := ctrl.SessionID()
	var sender network.Sender
	if sessionID != "" {
		sender, err = network.NewUDPSenderWithSession(cfg.Target, cfg.Port, limiter, cfg.SampleBytes, cfg.ChunkSize, sessionID)
	} else {
		sender, err = network.NewUDPSender(cfg.Target, cfg.Port, limiter, cfg.SampleBytes, cfg.ChunkSize)
	}
	if err != nil {
		return Result{}, err
	}
	defer sender.Close()

	exec := newSenderExecutor(sender)
	runResult, err := runSampleSeries(ctx, cfg, progress, exec, ctrl, DirectionUpload, reverseStartConfig{})
	if err != nil {
		return Result{}, err
	}

	stats, err := sender.Stats()
	if err != nil {
		return Result{}, err
	}
	if stats.UDP == nil {
		return Result{}, errors.New("missing udp stats")
	}
	udpStats := *stats.UDP

	var recv uint64
	var lost uint64
	var bytesRecv uint64
	for _, report := range runResult.reports {
		recv += report.PacketsRecv
		lost += report.PacketsLost
		bytesRecv += report.TotalBytes
	}

	totalRecv := recv + lost
	lossRate := 0.0
	if totalRecv > 0 {
		lossRate = float64(lost) / float64(totalRecv)
	}
	trimmed := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.trimmed })
	peak1s := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.peak1s })
	p90 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p90 })
	p80 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p80 })
	achievedBps := trimmed
	if achievedBps <= 0 && runResult.duration.Seconds() > 0 {
		achievedBps = float64(runResult.bytesSent*8) / runResult.duration.Seconds()
	}
	util := achievedBps / cfg.BandwidthBps
	if util < 0 {
		util = 0
	}

	return Result{
		BytesSent:        runResult.bytesSent,
		Duration:         runResult.duration,
		TargetBps:        cfg.BandwidthBps,
		AchievedBps:      achievedBps,
		Utilization:      util,
		SamplesPlanned:   cfg.Samples,
		SamplesCompleted: runResult.samplesCompleted,
		Network:          "udp",
		LossRate:         lossRate,
		PacketsSent:      udpStats.PacketsSent,
		PacketsRecv:      recv,
		PacketsLost:      lost,
		BytesRecv:        bytesRecv,
		Peak1sBps:        peak1s,
		TrimmedMeanBps:   trimmed,
		P90Bps:           p90,
		P80Bps:           p80,
	}, nil
}

func waitForRTTSample(sampler *metrics.RTTSampler, timeout time.Duration) time.Duration {
	if sampler == nil || timeout <= 0 {
		return 0
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stats := sampler.Stats()
		if stats.Samples > 0 && stats.Mean > 0 {
			return stats.Mean
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}
