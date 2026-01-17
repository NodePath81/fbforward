package engine

import "time"

// Config defines parameters for a network quality test.
type Config struct {
	Target       string
	Port         int
	Network      string
	BandwidthBps float64
	Direction    Direction
	Samples      int
	SampleBytes  int64
	Wait         time.Duration
	MaxDuration  time.Duration
	RTTRate      int
	ChunkSize    int64
}

// Direction describes traffic flow relative to the client.
type Direction int

const (
	DirectionUpload Direction = iota
	DirectionDownload
)

func (d Direction) String() string {
	switch d {
	case DirectionDownload:
		return "download"
	default:
		return "upload"
	}
}

// Result holds test output metrics.
type Result struct {
	BytesSent        int64
	Duration         time.Duration
	TargetBps        float64
	AchievedBps      float64
	Utilization      float64
	SamplesPlanned   int
	SamplesCompleted int

	RTTMean    time.Duration
	RTTMin     time.Duration
	RTTMax     time.Duration
	RTTStdDev  time.Duration
	RTTSamples int

	Network  string
	LossRate float64

	Retransmits        uint64
	SegmentsSent       uint64
	TCPSendBufferBytes uint64

	PacketsLost uint64
	PacketsRecv uint64
	PacketsSent uint64
	BytesRecv   uint64

	Peak1sBps      float64
	TrimmedMeanBps float64
	P90Bps         float64
	P80Bps         float64
}

// Progress captures sampling progress.
type Progress struct {
	SampleNum      int
	SampleProgress float64
	SampleBytes    int64
}

// ProgressFunc reports progress during sampling.
type ProgressFunc func(update Progress)
