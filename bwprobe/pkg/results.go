package probe

import "time"

// Results contains the complete test results.
type Results struct {
	// Throughput holds bandwidth measurements derived from server intervals.
	Throughput Throughput
	// RTT contains mean/min/max and jitter statistics.
	RTT RTTStats
	// Loss contains retransmit or packet-loss statistics.
	Loss LossStats
	// TestDuration is the wall-clock duration of the entire run.
	TestDuration time.Duration
	// BytesSent is the payload bytes sent by the client (upload tests).
	BytesSent int64
	// BytesReceived is the payload bytes received by the client (download tests).
	BytesReceived int64
	// SamplesPlanned is the requested number of samples.
	SamplesPlanned int
	// SamplesCompleted is the number of samples that completed.
	SamplesCompleted int
	// Network is "tcp" or "udp".
	Network string
	// TCPSendBufferBytes is the sender-side TCP buffer used (if available).
	TCPSendBufferBytes uint64
}

// Throughput contains bandwidth measurements in bits per second.
type Throughput struct {
	// TargetBps is the configured target rate.
	TargetBps int64
	// AchievedBps is the reported bandwidth (trimmed mean by default).
	AchievedBps float64
	// Utilization is AchievedBps / TargetBps.
	Utilization float64
	// TrimmedMeanBps is the trimmed mean of interval rates.
	TrimmedMeanBps float64
	// Peak1sBps is the sustained peak over a 1s rolling window.
	Peak1sBps float64
	// P90Bps is the 90th percentile of interval rates.
	P90Bps float64
	// P80Bps is the 80th percentile of interval rates.
	P80Bps float64
}

// RTTStats contains RTT measurements.
type RTTStats struct {
	// Min is the minimum RTT sample.
	Min time.Duration
	// Mean is the average RTT.
	Mean time.Duration
	// Max is the maximum RTT sample.
	Max time.Duration
	// Jitter is the standard deviation of RTT samples.
	Jitter time.Duration
	// Samples is the number of RTT samples collected.
	Samples int
}

// LossStats contains loss or retransmit statistics.
type LossStats struct {
	// Protocol is "tcp" or "udp".
	Protocol string
	// LossRate is retransmits/segments (TCP) or packets_lost/packets_sent (UDP).
	LossRate float64
	// Retransmits is the number of TCP retransmits (sender side).
	Retransmits uint64
	// SegmentsSent is the total TCP segments sent (sender side).
	SegmentsSent uint64
	// PacketsLost is the number of UDP packets lost (server side).
	PacketsLost uint64
	// PacketsRecv is the number of UDP packets received (server side).
	PacketsRecv uint64
	// PacketsSent is the number of UDP packets sent.
	PacketsSent uint64
}
