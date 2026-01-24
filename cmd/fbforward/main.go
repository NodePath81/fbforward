package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/NodePath81/fbforward/internal/app"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/NodePath81/fbforward/internal/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "run":
			runCmd := flag.NewFlagSet("run", flag.ExitOnError)
			configPath := runCmd.String("config", "config.yaml", "Path to config file")
			_ = runCmd.Parse(os.Args[2:])
			if *configPath == "config.yaml" && runCmd.NArg() > 0 {
				*configPath = runCmd.Arg(0)
			}
			runForwarder(*configPath)
			return
		case "check":
			checkCmd := flag.NewFlagSet("check", flag.ExitOnError)
			configPath := checkCmd.String("config", "config.yaml", "Path to config file")
			_ = checkCmd.Parse(os.Args[2:])
			if *configPath == "config.yaml" && checkCmd.NArg() > 0 {
				*configPath = checkCmd.Arg(0)
			}
			checkConfig(*configPath)
			return
		case "help", "-h", "--help":
			printHelp()
			return
		case "version", "-v", "--version":
			fmt.Println(version.Version)
			return
		}
	}

	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()
	if *configPath == "config.yaml" && len(flag.Args()) > 0 {
		*configPath = flag.Arg(0)
	}
	runForwarder(*configPath)
}

func runForwarder(configPath string) {
	logger := util.NewLogger()
	if runtime.GOOS != "linux" {
		logger.Error("unsupported OS", "goos", runtime.GOOS)
		os.Exit(1)
	}
	supervisor := app.NewSupervisor(configPath, logger)
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

func checkConfig(path string) {
	cfg, err := config.LoadConfig(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config invalid: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("config valid: %d upstreams, %d listeners\n", len(cfg.Upstreams), len(cfg.Forwarding.Listeners))
	os.Exit(0)
}

func printHelp() {
	fmt.Print(`fbforward - TCP/UDP port forwarder

Usage:
  fbforward run --config <path>   Start the forwarder
  fbforward check --config <path> Validate config file
  fbforward help                  Show this help
  fbforward version               Print version

Legacy:
  fbforward --config <path>
  fbforward <config-path>
`)
}
