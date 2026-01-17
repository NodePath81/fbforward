package probe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/engine"
	"github.com/NodePath81/fbforward/bwprobe/internal/network"
)

// Sampler provides low-level, per-sample control over a test session.
// It is intended for advanced callers who need manual sample orchestration.
// Reverse (download) mode is not supported by Sampler.
type Sampler struct {
	config    Config
	ctrl      controlClient
	sender    network.Sender
	sampleID  uint32
	lastStats *Results
}

type controlClient interface {
	SessionID() string
	StartSample(sampleID uint32, network string) error
	StopSample(sampleID uint32) (engine.SampleReport, error)
	Close() error
}

// NewSampler creates a new sampler instance with validated configuration.
func NewSampler(cfg Config) (*Sampler, error) {
	if cfg.Target == "" {
		return nil, errors.New("target is required")
	}
	if cfg.Reverse {
		return nil, errors.New("reverse mode is not supported by sampler")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultProbePort
	}
	if cfg.Port <= 0 {
		return nil, errors.New("port must be > 0")
	}
	if cfg.Network == "" {
		cfg.Network = DefaultNetwork
	}
	if cfg.ChunkSize == 0 {
		cfg.ChunkSize = DefaultChunkSize
	}
	return &Sampler{config: cfg}, nil
}

// Connect establishes the control channel and data sender.
func (s *Sampler) Connect(ctx context.Context) error {
	if s.sender != nil || s.ctrl != nil {
		return nil
	}

	networkName := strings.ToLower(strings.TrimSpace(s.config.Network))
	if networkName == "" {
		networkName = DefaultNetwork
	}
	if networkName != "tcp" && networkName != "udp" {
		return fmt.Errorf("invalid network %q (must be tcp or udp)", networkName)
	}

	ctrl, err := engine.NewControlClient(s.config.Target, s.config.Port)
	if err != nil {
		return err
	}
	s.ctrl = ctrl

	if networkName == "tcp" {
		sender, err := network.NewTCPSender(s.config.Target, s.config.Port, float64(s.config.BandwidthBps), 0, s.config.ChunkSize)
		if err != nil {
			_ = s.ctrl.Close()
			s.ctrl = nil
			return err
		}
		s.sender = sender
	} else {
		limiter := network.New(float64(s.config.BandwidthBps) / 8)
		sessionID := s.ctrl.SessionID()
		var sender network.Sender
		if sessionID != "" {
			sender, err = network.NewUDPSenderWithSession(s.config.Target, s.config.Port, limiter, s.config.SampleBytes, s.config.ChunkSize, sessionID)
		} else {
			sender, err = network.NewUDPSender(s.config.Target, s.config.Port, limiter, s.config.SampleBytes, s.config.ChunkSize)
		}
		if err != nil {
			_ = s.ctrl.Close()
			s.ctrl = nil
			return err
		}
		s.sender = sender
	}

	return nil
}

// SendSample sends a single sample of the requested size and stores the
// server report internally for retrieval via GetMetrics.
func (s *Sampler) SendSample(ctx context.Context, bytes int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.sender == nil || s.ctrl == nil {
		if err := s.Connect(ctx); err != nil {
			return err
		}
	}
	if bytes <= 0 {
		return errors.New("bytes must be > 0")
	}

	s.sampleID++
	if err := s.ctrl.StartSample(s.sampleID, s.config.Network); err != nil {
		return err
	}
	s.sender.SetSampleID(s.sampleID)

	var sent int64
	for sent < bytes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		remaining := bytes - sent
		n, err := s.sender.Send(remaining)
		if err != nil {
			return err
		}
		sent += int64(n)
	}

	report, err := s.ctrl.StopSample(s.sampleID)
	if err != nil {
		return err
	}

	duration := time.Duration(report.TotalDuration * float64(time.Second))
	achieved := 0.0
	if report.TotalDuration > 0 {
		achieved = float64(report.TotalBytes*8) / report.TotalDuration
	}

	res := Results{
		Throughput: Throughput{
			TargetBps:   s.config.BandwidthBps,
			AchievedBps: achieved,
		},
		TestDuration:     duration,
		BytesSent:        sent,
		BytesReceived:    int64(report.TotalBytes),
		SamplesPlanned:   1,
		SamplesCompleted: 1,
		Network:          strings.ToLower(strings.TrimSpace(s.config.Network)),
	}
	res.Loss.Protocol = res.Network
	res.Loss.PacketsRecv = report.PacketsRecv
	res.Loss.PacketsLost = report.PacketsLost
	if total := report.PacketsRecv + report.PacketsLost; total > 0 {
		res.Loss.LossRate = float64(report.PacketsLost) / float64(total)
	}

	s.lastStats = &res
	return nil
}

// GetMetrics returns the most recent sample metrics.
func (s *Sampler) GetMetrics(ctx context.Context) (*Results, error) {
	if s.lastStats == nil {
		return nil, errors.New("no metrics available")
	}
	return s.lastStats, nil
}

// Close closes the control channel and data sender.
func (s *Sampler) Close() error {
	if s.sender != nil {
		_ = s.sender.Close()
		s.sender = nil
	}
	if s.ctrl != nil {
		_ = s.ctrl.Close()
		s.ctrl = nil
	}
	return nil
}
