//go:build !linux

package shaping

import (
	"errors"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/util"
)

// UpstreamShapingEntry holds upstream config with resolved IPs for shaping.
type UpstreamShapingEntry struct {
	Tag           string
	IPs           []string
	UploadLimit   string
	DownloadLimit string
}

type TrafficShaper struct {
	enabled bool
}

func NewTrafficShaper(cfg config.ShapingConfig, _ []config.ListenerConfig, _ []UpstreamShapingEntry, _ util.Logger) *TrafficShaper {
	return &TrafficShaper{enabled: cfg.Enabled}
}

func (s *TrafficShaper) Apply() error {
	if !s.enabled {
		return nil
	}
	return errors.New("traffic shaping is only supported on linux")
}

func (s *TrafficShaper) UpdateUpstreams(_ []UpstreamShapingEntry) error {
	return s.Apply()
}

func (s *TrafficShaper) Cleanup() error {
	return nil
}
