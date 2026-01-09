//go:build !linux

package main

import "errors"

// UpstreamShapingEntry holds upstream config with resolved IPs for shaping.
type UpstreamShapingEntry struct {
	Tag     string
	IPs     []string
	Ingress *BandwidthConfig
	Egress  *BandwidthConfig
}

type TrafficShaper struct {
	enabled bool
}

func NewTrafficShaper(cfg ShapingConfig, _ []ListenerConfig, _ []UpstreamShapingEntry, _ Logger) *TrafficShaper {
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
