package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(Version)
		return
	}
	flag.Parse()
	if *configPath == "config.yaml" && len(flag.Args()) > 0 {
		*configPath = flag.Arg(0)
	}

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
