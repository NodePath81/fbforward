//go:build !linux

package main

import "errors"

type TrafficShaper struct {
	enabled bool
}

func NewTrafficShaper(cfg ShapingConfig, _ []ListenerConfig, _ Logger) *TrafficShaper {
	return &TrafficShaper{enabled: cfg.Enabled}
}

func (s *TrafficShaper) Apply() error {
	if !s.enabled {
		return nil
	}
	return errors.New("traffic shaping is only supported on linux")
}

func (s *TrafficShaper) Cleanup() error {
	return nil
}
