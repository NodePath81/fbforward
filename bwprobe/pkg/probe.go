package probe

import (
	"context"
	"fmt"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/engine"
	"github.com/NodePath81/fbforward/bwprobe/internal/util"
)

// ProgressFunc is called periodically during test execution.
// phase identifies the current sample (for example, "sample 2/10"),
// percentComplete is in the range [0,1] for that sample, and status is a
// human-readable string (for example, "120 Mbps | 15.0 MB").
type ProgressFunc func(phase string, percentComplete float64, status string)

// Run executes a complete network quality test with default progress handling.
func Run(ctx context.Context, cfg Config) (*Results, error) {
	return RunWithProgress(ctx, cfg, nil)
}

// RunWithProgress executes a test and reports progress through the callback.
func RunWithProgress(ctx context.Context, cfg Config, progress ProgressFunc) (*Results, error) {
	if cfg.Port == 0 {
		cfg.Port = DefaultProbePort
	}
	if cfg.Network == "" {
		cfg.Network = DefaultNetwork
	}
	if cfg.RTTRate == 0 {
		cfg.RTTRate = DefaultRTTRate
	}
	if cfg.Samples == 0 {
		cfg.Samples = DefaultSamples
	}
	if cfg.ChunkSize == 0 {
		cfg.ChunkSize = DefaultChunkSize
	}
	engCfg := engine.Config{
		Target:       cfg.Target,
		Port:         cfg.Port,
		Network:      cfg.Network,
		BandwidthBps: float64(cfg.BandwidthBps),
		Direction:    directionFromReverse(cfg.Reverse),
		Samples:      cfg.Samples,
		SampleBytes:  cfg.SampleBytes,
		Wait:         cfg.Wait,
		MaxDuration:  cfg.MaxDuration,
		RTTRate:      cfg.RTTRate,
		ChunkSize:    cfg.ChunkSize,
	}

	var engProgress engine.ProgressFunc
	if progress != nil {
		var lastSample int
		var lastBytes int64
		var lastTime time.Time
		var sampleStart time.Time
		var smoothRate float64
		engProgress = func(update engine.Progress) {
			phase := fmt.Sprintf("sample %d/%d", update.SampleNum, engCfg.Samples)
			now := time.Now()
			instant := 0.0
			if update.SampleNum != lastSample {
				lastSample = update.SampleNum
				lastBytes = update.SampleBytes
				lastTime = now
				sampleStart = now
				smoothRate = 0
			} else if !lastTime.IsZero() {
				delta := now.Sub(lastTime)
				deltaBytes := update.SampleBytes - lastBytes
				elapsed := now.Sub(sampleStart)
				if delta >= 150*time.Millisecond && deltaBytes > 0 {
					instant = float64(deltaBytes*8) / delta.Seconds()
					lastBytes = update.SampleBytes
					lastTime = now
				} else if elapsed >= 200*time.Millisecond && update.SampleBytes > 0 {
					instant = float64(update.SampleBytes*8) / elapsed.Seconds()
				}
			}

			if instant > 0 {
				if smoothRate == 0 {
					smoothRate = instant
				} else {
					alpha := 0.2
					smoothRate = smoothRate*(1-alpha) + instant*alpha
				}
			}

			status := fmt.Sprintf("%s | %s", util.FormatBitsPerSecond(smoothRate), util.FormatBytes(float64(update.SampleBytes)))
			progress(phase, update.SampleProgress, status)
		}
	}

	result, err := engine.Run(ctx, engCfg, engProgress)
	if err != nil {
		return nil, err
	}
	out := convertResult(cfg, result)
	return &out, nil
}

func directionFromReverse(reverse bool) engine.Direction {
	if reverse {
		return engine.DirectionDownload
	}
	return engine.DirectionUpload
}

func convertResult(cfg Config, result engine.Result) Results {
	return Results{
		Throughput: Throughput{
			TargetBps:      cfg.BandwidthBps,
			AchievedBps:    result.AchievedBps,
			Utilization:    result.Utilization,
			TrimmedMeanBps: result.TrimmedMeanBps,
			Peak1sBps:      result.Peak1sBps,
			P90Bps:         result.P90Bps,
			P80Bps:         result.P80Bps,
		},
		RTT: RTTStats{
			Min:     result.RTTMin,
			Mean:    result.RTTMean,
			Max:     result.RTTMax,
			Jitter:  result.RTTStdDev,
			Samples: result.RTTSamples,
		},
		Loss: LossStats{
			Protocol:     result.Network,
			LossRate:     result.LossRate,
			Retransmits:  result.Retransmits,
			SegmentsSent: result.SegmentsSent,
			PacketsLost:  result.PacketsLost,
			PacketsRecv:  result.PacketsRecv,
			PacketsSent:  result.PacketsSent,
		},
		TestDuration:       result.Duration,
		BytesSent:          result.BytesSent,
		BytesReceived:      int64(result.BytesRecv),
		SamplesPlanned:     result.SamplesPlanned,
		SamplesCompleted:   result.SamplesCompleted,
		Network:            result.Network,
		TCPSendBufferBytes: result.TCPSendBufferBytes,
	}
}
