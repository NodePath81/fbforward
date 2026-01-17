package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"strings"
	"time"
)

const (
	trimFraction  = 0.1
	peakWindowDur = time.Second
)

type sampleMetrics struct {
	peak1s  float64
	trimmed float64
	p90     float64
	p80     float64
}

type sampleRunResult struct {
	bytesSent        int64
	bytesRecv        int64
	samplesCompleted int
	duration         time.Duration
	reports          []SampleReport
	metrics          []sampleMetrics
	udpStats         udpRecvStats
}

type udpRecvStats struct {
	recv  uint64
	lost  uint64
	bytes uint64
}

func runSampleSeries(ctx context.Context, cfg Config, progress ProgressFunc, exec SampleExecutor, ctrl *controlClient, direction Direction, reverseCfg reverseStartConfig) (sampleRunResult, error) {
	start := time.Now()
	var bytesTransferred int64
	samplesCompleted := 0
	updateStride := cfg.SampleBytes / 100
	if updateStride < 1 {
		updateStride = 1
	}

	result := sampleRunResult{}

	for sample := 0; sample < cfg.Samples; sample++ {
		if ctx != nil && ctx.Err() != nil {
			return result, ctx.Err()
		}
		if cfg.MaxDuration > 0 && time.Since(start) >= cfg.MaxDuration {
			break
		}

		sampleID := uint32(sample + 1)
		exec.SetSampleID(sampleID)
		if direction == DirectionDownload {
			if err := ctrl.StartSampleReverse(sampleID, reverseCfg); err != nil {
				return result, err
			}
		} else {
			if err := ctrl.StartSample(sampleID, cfg.Network); err != nil {
				return result, err
			}
		}

		var sampleBytes int64
		nextUpdate := updateStride
		var deadline time.Time
		if direction == DirectionDownload {
			expectedDur := time.Duration(0)
			if cfg.BandwidthBps > 0 {
				expectedDur = time.Duration(float64(cfg.SampleBytes*8) / cfg.BandwidthBps * float64(time.Second))
			}
			if expectedDur <= 0 {
				expectedDur = 10 * time.Second
			}
			if strings.ToLower(cfg.Network) == "udp" {
				deadline = time.Now().Add(expectedDur + 2*reverseReadTimeout)
			} else {
				deadline = time.Now().Add(expectedDur + 5*time.Second)
			}
		}

		timeoutCount := 0
		const maxTimeouts = 10

		for sampleBytes < cfg.SampleBytes {
			if ctx != nil && ctx.Err() != nil {
				return result, ctx.Err()
			}
			if cfg.MaxDuration > 0 && time.Since(start) >= cfg.MaxDuration {
				break
			}
			if !deadline.IsZero() && time.Now().After(deadline) {
				return result, fmt.Errorf("sample deadline exceeded after %v", time.Since(start))
			}

			remaining := cfg.SampleBytes - sampleBytes
			n, err := exec.Transfer(remaining)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					timeoutCount++
					if timeoutCount >= maxTimeouts {
						return result, fmt.Errorf("too many timeouts (%d): %w", maxTimeouts, err)
					}
					continue
				}
				return result, err
			}
			if n <= 0 {
				if direction == DirectionUpload {
					return result, errors.New("short write")
				}
				continue
			}
			bytesTransferred += int64(n)
			sampleBytes += int64(n)

			if sampleBytes >= nextUpdate || sampleBytes >= cfg.SampleBytes {
				sampleProgress := float64(sampleBytes) / float64(cfg.SampleBytes)
				if progress != nil {
					progress(Progress{
						SampleNum:      sample + 1,
						SampleProgress: sampleProgress,
						SampleBytes:    sampleBytes,
					})
				}
				for nextUpdate <= sampleBytes {
					nextUpdate += updateStride
				}
			}
		}

		report, err := ctrl.StopSample(sampleID)
		if err != nil {
			return result, err
		}
		result.reports = append(result.reports, report)
		metrics := sampleMetricsFromReport(report)
		result.metrics = append(result.metrics, metrics)
		if stats, ok := exec.(SampleStatsProvider); ok {
			recv, lost, bytes := stats.SampleStats()
			result.udpStats.recv += recv
			result.udpStats.lost += lost
			result.udpStats.bytes += bytes
		}

		samplesCompleted++
		if progress != nil {
			progress(Progress{
				SampleNum:      sample + 1,
				SampleProgress: 1.0,
				SampleBytes:    sampleBytes,
			})
		}

		if sample < cfg.Samples-1 && cfg.Wait > 0 {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return result, ctx.Err()
				case <-time.After(cfg.Wait):
				}
			} else {
				time.Sleep(cfg.Wait)
			}
		}
	}

	duration := time.Since(start)
	if duration <= 0 {
		duration = time.Millisecond
	}

	result.samplesCompleted = samplesCompleted
	result.duration = duration
	if direction == DirectionDownload {
		result.bytesRecv = bytesTransferred
	} else {
		result.bytesSent = bytesTransferred
	}
	return result, nil
}

func sampleMetricsFromReport(report SampleReport) sampleMetrics {
	intervals := report.Intervals
	if len(intervals) == 0 {
		fallback := report.AvgThroughput
		if fallback <= 0 && report.TotalDuration > 0 {
			fallback = float64(report.TotalBytes*8) / report.TotalDuration
		}
		return sampleMetrics{
			peak1s:  fallback,
			trimmed: fallback,
			p90:     fallback,
			p80:     fallback,
		}
	}

	throughputs := make([]float64, 0, len(intervals))
	cumulativeBytes := make([]float64, 0, len(intervals))
	cumulativeTimes := make([]float64, 0, len(intervals))
	var totalBytes float64
	var totalTime float64
	for _, interval := range intervals {
		if interval.DurationMs <= 0 {
			continue
		}
		seconds := float64(interval.DurationMs) / 1000.0
		if seconds <= 0 {
			continue
		}
		throughputs = append(throughputs, float64(interval.Bytes*8)/seconds)
		totalBytes += float64(interval.Bytes)
		totalTime += seconds
		cumulativeBytes = append(cumulativeBytes, totalBytes)
		cumulativeTimes = append(cumulativeTimes, totalTime)
	}
	if len(throughputs) == 0 || totalTime <= 0 {
		return sampleMetrics{}
	}

	sort.Float64s(throughputs)

	trimmed := trimmedMean(throughputs, trimFraction)
	p90 := percentile(throughputs, 0.90)
	p80 := percentile(throughputs, 0.80)
	peak := peakRollingWindow(cumulativeBytes, cumulativeTimes, peakWindowDur)
	if peak <= 0 && report.TotalDuration > 0 {
		peak = float64(report.TotalBytes*8) / report.TotalDuration
	}

	return sampleMetrics{
		peak1s:  peak,
		trimmed: trimmed,
		p90:     p90,
		p80:     p80,
	}
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func meanMetric(metrics []sampleMetrics, pick func(sampleMetrics) float64) float64 {
	if len(metrics) == 0 {
		return 0
	}
	sum := 0.0
	for _, metric := range metrics {
		sum += pick(metric)
	}
	return sum / float64(len(metrics))
}

func trimmedMean(values []float64, frac float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if frac <= 0 {
		return mean(values)
	}
	if frac >= 0.5 {
		return mean(values)
	}
	cut := int(math.Floor(float64(len(values)) * frac))
	start := cut
	end := len(values) - cut
	if start >= end {
		return mean(values)
	}
	sum := 0.0
	for i := start; i < end; i++ {
		sum += values[i]
	}
	return sum / float64(end-start)
}

func percentile(values []float64, pct float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if pct <= 0 {
		return values[0]
	}
	if pct >= 1 {
		return values[len(values)-1]
	}
	idx := int(math.Ceil(float64(len(values))*pct)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func peakRollingWindow(bytes []float64, times []float64, window time.Duration) float64 {
	if len(bytes) == 0 || len(times) == 0 || len(bytes) != len(times) {
		return 0
	}
	windowSec := window.Seconds()
	if windowSec <= 0 {
		return 0
	}

	start := 0
	peak := 0.0
	for i := 0; i < len(times); i++ {
		for start+1 <= i && times[i]-times[start+1] >= windowSec {
			start++
		}
		dt := times[i] - times[start]
		if dt < windowSec || dt <= 0 {
			continue
		}
		db := bytes[i] - bytes[start]
		if db < 0 {
			continue
		}
		rate := db * 8 / dt
		if rate > peak {
			peak = rate
		}
	}
	return peak
}
