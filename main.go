package main

import (
	"flag"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	logger := NewLogger()
	if runtime.GOOS != "linux" {
		logger.Error("unsupported OS", "goos", runtime.GOOS)
		os.Exit(1)
	}
	supervisor := NewSupervisor(*configPath, logger)
	if err := supervisor.Start(); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutdown requested")
	supervisor.Stop()
}
