package probe

import "time"

const (
	// DefaultProbePort is the default port for control and data connections.
	DefaultProbePort = 9876
	// DefaultNetwork is the default network protocol ("tcp").
	DefaultNetwork = "tcp"
	// DefaultRTTRate is the default RTT sample rate (samples per second).
	DefaultRTTRate = 10
	// DefaultSamples is the default number of samples to run per test.
	DefaultSamples = 10
	// DefaultChunkSize is the default chunk size in bytes (includes headers).
	DefaultChunkSize = 1200
)

// Config defines parameters for a network quality test.
type Config struct {
	// Target is the host or IP of the server to test.
	Target string
	// Port is the control/data port on the server.
	Port int
	// Network selects the protocol ("tcp" or "udp").
	Network string
	// BandwidthBps is the target bandwidth cap in bits per second.
	BandwidthBps int64
	// Reverse requests a download test (server -> client).
	Reverse bool
	// Samples is the number of samples to run.
	Samples int
	// SampleBytes is the payload bytes per sample (headers excluded).
	SampleBytes int64
	// Wait is the pause between samples.
	Wait time.Duration
	// MaxDuration caps the total test duration (0 = unlimited).
	MaxDuration time.Duration
	// RTTRate is the RTT sampling rate in samples per second.
	RTTRate int
	// ChunkSize is the total chunk size including protocol headers.
	ChunkSize int64
}

// RTTConfig defines parameters for RTT-only measurement.
type RTTConfig struct {
	// Target is the host or IP to measure.
	Target string
	// Port is the port to probe.
	Port int
	// Network selects the protocol ("tcp" or "udp").
	Network string
	// Samples is the number of RTT samples to collect.
	Samples int
	// Rate is the RTT sampling rate in samples per second.
	Rate int
	// Timeout bounds each ping probe.
	Timeout time.Duration
}
