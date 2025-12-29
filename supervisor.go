package main

import (
	"sync"
)

type Supervisor struct {
	configPath string
	logger     Logger
	mu         sync.Mutex
	runtime    *Runtime
}

func NewSupervisor(configPath string, logger Logger) *Supervisor {
	return &Supervisor{
		configPath: configPath,
		logger:     logger,
	}
}

func (s *Supervisor) Start() error {
	cfg, err := LoadConfig(s.configPath)
	if err != nil {
		return err
	}
	runtime, err := NewRuntime(cfg, s.logger, s.Restart)
	if err != nil {
		return err
	}
	if err := runtime.Start(); err != nil {
		runtime.Stop()
		return err
	}
	s.mu.Lock()
	s.runtime = runtime
	s.mu.Unlock()
	return nil
}

func (s *Supervisor) Restart() error {
	s.mu.Lock()
	current := s.runtime
	s.runtime = nil
	s.mu.Unlock()

	if current != nil {
		current.Stop()
	}
	return s.Start()
}

func (s *Supervisor) Stop() {
	s.mu.Lock()
	current := s.runtime
	s.runtime = nil
	s.mu.Unlock()
	if current != nil {
		current.Stop()
	}
}
