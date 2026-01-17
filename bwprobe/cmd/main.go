package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/network"
	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
	"github.com/NodePath81/fbforward/bwprobe/internal/server"
	"github.com/NodePath81/fbforward/bwprobe/internal/util"
	"github.com/NodePath81/fbforward/bwprobe/pkg"
)

func main() {
	mode := flag.String("mode", "client", "Mode: server or client")
	port := flag.Int("port", probe.DefaultProbePort, "Port to use")
	target := flag.String("target", "localhost", "Target host (client mode)")
	networkName := flag.String("network", probe.DefaultNetwork, "Network protocol: tcp or udp")
	bandwidthInput := flag.String("bandwidth", "", "Target bandwidth cap (e.g., 100Mbps) (required)")
	sampleBytesInput := flag.String("sample-bytes", "", "Bytes to send per sampling interval (e.g., 5MB) (required)")
	samples := flag.Int("samples", probe.DefaultSamples, "Number of samples to send")
	wait := flag.Duration("wait", 0, "Wait time between samples")
	maxDuration := flag.Duration("max-duration", 0, "Maximum test duration (0 = unlimited)")
	rttRate := flag.Int("rtt-rate", probe.DefaultRTTRate, "RTT samples per second")
	chunkSizeInput := flag.String("chunk-size", fmt.Sprintf("%dB", probe.DefaultChunkSize), "Chunk size including header (e.g., 1.2KB, 64KB, 1MB)")
	reverse := flag.Bool("reverse", false, "Reverse test direction (server -> client)")
	noProgress := flag.Bool("no-progress", false, "Disable progress bar")
	recvWait := flag.Duration("recv-wait", 500*time.Millisecond, "Server receive window after sample stop (0 = no wait)")
	flag.Parse()

	if *mode == "server" {
		server.Run(server.Config{
			Port:     *port,
			RecvWait: *recvWait,
		})
		return
	}

	if *bandwidthInput == "" {
		fmt.Fprintln(os.Stderr, "error: -bandwidth is required")
		os.Exit(1)
	}
	if *sampleBytesInput == "" {
		fmt.Fprintln(os.Stderr, "error: -sample-bytes is required")
		os.Exit(1)
	}

	sampleBytes, err := util.ParseBytes(*sampleBytesInput)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	bwBps, err := util.ParseBandwidth(*bandwidthInput)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	chunkSize, err := util.ParseBytes(*chunkSizeInput)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	cfg := probe.Config{
		Target:       *target,
		Port:         *port,
		Network:      *networkName,
		BandwidthBps: int64(bwBps),
		Reverse:      *reverse,
		Samples:      *samples,
		SampleBytes:  sampleBytes,
		Wait:         *wait,
		MaxDuration:  *maxDuration,
		RTTRate:      *rttRate,
		ChunkSize:    chunkSize,
	}

	fmt.Println("=== Network Quality Test (Client) ===")
	fmt.Printf("Target: %s:%d\n", cfg.Target, cfg.Port)
	networkLower := strings.ToLower(cfg.Network)
	fmt.Printf("Protocol: %s\n", strings.ToUpper(cfg.Network))
	fmt.Println()
	fmt.Println("Test Configuration:")
	fmt.Printf("  Target bandwidth:  %s\n", util.FormatBitsPerSecond(float64(cfg.BandwidthBps)))
	fmt.Printf("  Role:              Client (requester)\n")
	if cfg.Reverse {
		fmt.Printf("  Traffic direction: Download (server -> client)\n")
	} else {
		fmt.Printf("  Traffic direction: Upload (client -> server)\n")
	}
	fmt.Printf("  Samples:           %d\n", cfg.Samples)
	fmt.Printf("  Sample bytes:      %s\n", util.FormatBytes(float64(cfg.SampleBytes)))
	fmt.Printf("  Wait:              %s\n", cfg.Wait)
	if cfg.MaxDuration > 0 {
		fmt.Printf("  Max duration:      %s\n", cfg.MaxDuration)
	} else {
		fmt.Printf("  Max duration:      Unlimited\n")
	}
	fmt.Printf("  RTT sample rate:   %d samples/sec\n", cfg.RTTRate)

	if networkLower == "tcp" {
		payloadSize, err := network.TCPPayloadSize(cfg.ChunkSize)
		if err == nil {
			rttEstimate := time.Duration(0)
			rttSamples := cfg.RTTRate
			if rttSamples < 3 {
				rttSamples = 3
			}
			rttStats, err := probe.MeasureRTT(context.Background(), probe.RTTConfig{
				Target:  cfg.Target,
				Port:    cfg.Port,
				Network: cfg.Network,
				Samples: rttSamples,
				Rate:    cfg.RTTRate,
				Timeout: time.Second,
			})
			if err == nil && rttStats != nil && rttStats.Samples > 0 {
				rttEstimate = rttStats.Mean
			}
			bufBytes := network.TCPWriteBufferBytes(float64(cfg.BandwidthBps), rttEstimate, payloadSize)
			fmt.Printf("  TCP send buffer (est.): %s\n", util.FormatBytes(float64(bufBytes)))
			fmt.Printf("  Chunk size:        %s\n", util.FormatBytes(float64(payloadSize+protocol.TCPFrameHeaderSize)))
		}
	} else {
		_, totalSize, err := network.UDPPayloadSize(cfg.ChunkSize)
		if err == nil {
			fmt.Printf("  Chunk size:        %s\n", util.FormatBytes(float64(totalSize)))
			if int64(totalSize) < cfg.ChunkSize {
				fmt.Printf("  Chunk size (cfg):  %s\n", util.FormatBytes(float64(cfg.ChunkSize)))
			}
			fmt.Printf("  UDP recv buffer:   %s\n", util.FormatBytes(float64(protocol.UDPMaxChunkSize)))
		}
	}

	var lastUpdate time.Time
	progressFn := func(phase string, percent float64, status string) {
		if *noProgress {
			return
		}
		if percent < 1.0 && time.Since(lastUpdate) < 100*time.Millisecond {
			return
		}
		lastUpdate = time.Now()
		barWidth := 20
		filled := int(percent * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := ""
		for i := 0; i < barWidth; i++ {
			if i < filled {
				bar += "█"
			} else {
				bar += "░"
			}
		}
		if percent > 1 {
			percent = 1
		}
		fmt.Printf("\r[%s] %s %3.0f%% | %s", phase, bar, percent*100, status)
		if percent >= 1.0 {
			fmt.Print("\033[K")
		}
	}

	ctx := context.Background()
	results, err := probe.RunWithProgress(ctx, cfg, progressFn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if !*noProgress {
		fmt.Println()
	}

	fmt.Println()
	fmt.Println("Test Results:")
	fmt.Printf("  Duration:           %s\n", results.TestDuration)
	if cfg.Reverse {
		fmt.Printf("  Bytes received:     %s\n", util.FormatBytes(float64(results.BytesReceived)))
	} else {
		fmt.Printf("  Bytes sent:         %s\n", util.FormatBytes(float64(results.BytesSent)))
	}
	fmt.Printf("  Achieved bandwidth (trimmed mean): %s (%.1f%% of target)\n",
		util.FormatBitsPerSecond(results.Throughput.AchievedBps),
		results.Throughput.Utilization*100)
	fmt.Printf("  Samples:            %d/%d\n", results.SamplesCompleted, results.SamplesPlanned)
	if networkLower == "tcp" && results.TCPSendBufferBytes > 0 {
		if cfg.Reverse {
			fmt.Printf("  TCP send buffer:    %s (server)\n", util.FormatBytes(float64(results.TCPSendBufferBytes)))
		} else {
			fmt.Printf("  TCP send buffer:    %s\n", util.FormatBytes(float64(results.TCPSendBufferBytes)))
		}
	}
	fmt.Println()
	fmt.Println("Bandwidth Estimates (server intervals):")
	fmt.Printf("  Sustained peak (1s): %s\n", util.FormatBitsPerSecond(results.Throughput.Peak1sBps))
	fmt.Printf("  Trimmed mean:        %s\n", util.FormatBitsPerSecond(results.Throughput.TrimmedMeanBps))
	fmt.Printf("  P90:                 %s\n", util.FormatBitsPerSecond(results.Throughput.P90Bps))
	fmt.Printf("  P80:                 %s\n", util.FormatBitsPerSecond(results.Throughput.P80Bps))
	fmt.Println()
	fmt.Println("RTT Statistics:")
	fmt.Printf("  Mean:    %s\n", results.RTT.Mean)
	fmt.Printf("  Min:     %s\n", results.RTT.Min)
	fmt.Printf("  Max:     %s\n", results.RTT.Max)
	fmt.Printf("  Jitter:  %s (stdev)\n", results.RTT.Jitter)
	fmt.Printf("  Samples: %d\n", results.RTT.Samples)
	fmt.Println()

	if networkLower == "tcp" {
		fmt.Println("TCP Retransmits:")
		fmt.Printf("  Retransmits:  %d\n", results.Loss.Retransmits)
		fmt.Printf("  Segments sent: %d\n", results.Loss.SegmentsSent)
		fmt.Printf("  Loss rate:    %.4f%% (%d/%d)\n",
			results.Loss.LossRate*100, results.Loss.Retransmits, results.Loss.SegmentsSent)
	} else {
		fmt.Println("UDP Packet Loss:")
		fmt.Printf("  Packets sent:     %d\n", results.Loss.PacketsSent)
		fmt.Printf("  Packets received: %d\n", results.Loss.PacketsRecv)
		fmt.Printf("  Packets lost:     %d\n", results.Loss.PacketsLost)
		fmt.Printf("  Loss rate:        %.4f%%\n", results.Loss.LossRate*100)
	}
}
