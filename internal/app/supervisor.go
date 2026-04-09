package app

import (
	"log/slog"
	"sync"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/util"
)

type Supervisor struct {
	configPath string
	logger     util.Logger
	mu         sync.Mutex
	runtime    *Runtime
}

func NewSupervisor(configPath string, logger util.Logger) *Supervisor {
	return &Supervisor{
		configPath: configPath,
		logger:     logger,
	}
}

func (s *Supervisor) Start() error {
	lifecycleLogger := util.ComponentLogger(s.logger, util.CompLifecycle)
	cfg, err := config.LoadConfig(s.configPath)
	if err != nil {
		return err
	}
	for _, warning := range cfg.Warnings {
		util.Event(lifecycleLogger, slog.LevelWarn, "lifecycle.config_warning", "warning", warning)
	}
	runtime, err := NewRuntime(cfg, s.logger, s.Restart)
	if err != nil {
		return err
	}
	util.Event(lifecycleLogger, slog.LevelInfo, "lifecycle.config_summary",
		"upstream_count", len(cfg.Upstreams),
		"listener_count", len(cfg.Forwarding.Listeners),
		"upstream.mode", runtime.manager.Mode().String(),
	)
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
	lifecycleLogger := util.ComponentLogger(s.logger, util.CompLifecycle)
	util.Event(lifecycleLogger, slog.LevelInfo, "lifecycle.restart_triggered", "restart.source", "rpc")

	s.mu.Lock()
	current := s.runtime
	s.runtime = nil
	s.mu.Unlock()

	if current != nil {
		current.Stop()
	}
	if err := s.Start(); err != nil {
		util.Event(lifecycleLogger, slog.LevelError, "lifecycle.restart_failed", "error", err)
		return err
	}
	return nil
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
