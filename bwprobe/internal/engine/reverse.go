package engine

import (
	"context"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/transport"
)

const (
	reverseReadTimeout = 2 * time.Second
)

type reverseStartConfig struct {
	BandwidthBps float64
	ChunkSize    int64
	RTT          time.Duration
	SampleBytes  int64
	UDPPort      int
}

func runTCPReverse(ctx context.Context, cfg Config, progress ProgressFunc, rtt time.Duration) (Result, error) {
	ctrl, err := NewControlClient(cfg.Target, cfg.Port)
	if err != nil {
		return Result{}, err
	}
	defer ctrl.Close()

	recvConn, err := transport.DialReverseTCP(cfg.Target, cfg.Port, ctrl.SessionID())
	if err != nil {
		return Result{}, err
	}
	receiver := transport.NewTCPReceiver(recvConn, reverseReadTimeout)
	exec := newReceiverExecutor(receiver)
	defer exec.Close()
	reverseCfg := reverseStartConfig{
		BandwidthBps: cfg.BandwidthBps,
		ChunkSize:    cfg.ChunkSize,
		RTT:          rtt,
		SampleBytes:  cfg.SampleBytes,
		UDPPort:      0,
	}
	runResult, err := runSampleSeries(ctx, cfg, progress, exec, ctrl, DirectionDownload, reverseCfg)
	if err != nil {
		return Result{}, err
	}

	trimmed := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.trimmed })
	peak1s := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.peak1s })
	p90 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p90 })
	p80 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p80 })
	achievedBps := trimmed
	if achievedBps <= 0 && runResult.duration.Seconds() > 0 {
		achievedBps = float64(runResult.bytesRecv*8) / runResult.duration.Seconds()
	}
	util := achievedBps / cfg.BandwidthBps
	if util < 0 {
		util = 0
	}

	var maxRetrans uint64
	var maxSegs uint64
	var sendBuf uint64
	for _, report := range runResult.reports {
		if report.TCPRetransmits > maxRetrans {
			maxRetrans = report.TCPRetransmits
		}
		if report.TCPSegmentsSent > maxSegs {
			maxSegs = report.TCPSegmentsSent
		}
		if report.TCPSendBufferBytes > sendBuf {
			sendBuf = report.TCPSendBufferBytes
		}
	}
	loss := 0.0
	if maxSegs > 0 {
		loss = float64(maxRetrans) / float64(maxSegs)
	}

	return Result{
		BytesSent:          0,
		BytesRecv:          uint64(runResult.bytesRecv),
		Duration:           runResult.duration,
		TargetBps:          cfg.BandwidthBps,
		AchievedBps:        achievedBps,
		Utilization:        util,
		SamplesPlanned:     cfg.Samples,
		SamplesCompleted:   runResult.samplesCompleted,
		Network:            "tcp",
		LossRate:           loss,
		Retransmits:        maxRetrans,
		SegmentsSent:       maxSegs,
		TCPSendBufferBytes: sendBuf,
		Peak1sBps:          peak1s,
		TrimmedMeanBps:     trimmed,
		P90Bps:             p90,
		P80Bps:             p80,
	}, nil
}

func runUDPReverse(ctx context.Context, cfg Config, progress ProgressFunc) (Result, error) {
	ctrl, err := NewControlClient(cfg.Target, cfg.Port)
	if err != nil {
		return Result{}, err
	}
	defer ctrl.Close()

	recvConn, udpPort, err := transport.ListenReverseUDP()
	if err != nil {
		return Result{}, err
	}
	if err := transport.SendUDPHello(recvConn, cfg.Target, cfg.Port); err != nil {
		return Result{}, err
	}
	var regErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctrl.RegisterUDP(udpPort); err == nil {
			regErr = nil
			break
		} else {
			regErr = err
		}
		if attempt < 2 {
			_ = transport.SendUDPHello(recvConn, cfg.Target, cfg.Port)
			time.Sleep(100 * time.Millisecond)
		}
	}
	if regErr != nil {
		return Result{}, regErr
	}

	receiver := transport.NewUDPReceiver(recvConn, reverseReadTimeout)
	exec := newReceiverExecutor(receiver)
	defer exec.Close()
	reverseCfg := reverseStartConfig{
		BandwidthBps: cfg.BandwidthBps,
		ChunkSize:    cfg.ChunkSize,
		RTT:          0,
		SampleBytes:  cfg.SampleBytes,
		UDPPort:      udpPort,
	}
	runResult, err := runSampleSeries(ctx, cfg, progress, exec, ctrl, DirectionDownload, reverseCfg)
	if err != nil {
		return Result{}, err
	}

	trimmed := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.trimmed })
	peak1s := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.peak1s })
	p90 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p90 })
	p80 := meanMetric(runResult.metrics, func(m sampleMetrics) float64 { return m.p80 })
	achievedBps := trimmed
	if achievedBps <= 0 && runResult.duration.Seconds() > 0 {
		achievedBps = float64(runResult.bytesRecv*8) / runResult.duration.Seconds()
	}
	util := achievedBps / cfg.BandwidthBps
	if util < 0 {
		util = 0
	}

	totalRecv := runResult.udpStats.recv
	totalLost := runResult.udpStats.lost
	total := totalRecv + totalLost
	lossRate := 0.0
	if total > 0 {
		lossRate = float64(totalLost) / float64(total)
	}

	return Result{
		BytesSent:        0,
		BytesRecv:        uint64(runResult.bytesRecv),
		Duration:         runResult.duration,
		TargetBps:        cfg.BandwidthBps,
		AchievedBps:      achievedBps,
		Utilization:      util,
		SamplesPlanned:   cfg.Samples,
		SamplesCompleted: runResult.samplesCompleted,
		Network:          "udp",
		LossRate:         lossRate,
		PacketsSent:      total,
		PacketsRecv:      totalRecv,
		PacketsLost:      totalLost,
		Peak1sBps:        peak1s,
		TrimmedMeanBps:   trimmed,
		P90Bps:           p90,
		P80Bps:           p80,
	}, nil
}
