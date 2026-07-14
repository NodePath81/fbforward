package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/NodePath81/fbforward/internal/util"
	"github.com/NodePath81/fbforward/internal/version"
	"github.com/NodePath81/fbforward/pkg/fbmeasure"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			fmt.Println(version.Version)
			return
		case "help", "-h", "--help":
			printHelp()
			return
		}
	}

	listen := flag.String("listen", "127.0.0.1:9876", "TCP and UDP listen address")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text or json)")
	showVersion := flag.Bool("version", false, "Print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	logger := util.ComponentLogger(util.NewLogger(*logLevel, *logFormat), "fbmeasure")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	server, err := fbmeasure.NewServer(fbmeasure.ServerConfig{ListenAddress: *listen})
	if err != nil {
		util.Event(logger, slog.LevelError, "fbmeasure.start_failed", "error", err)
		os.Exit(1)
	}
	defer server.Close()
	util.Event(logger, slog.LevelInfo, "fbmeasure.ready", "listen.addr", server.Addr().String(), "version", version.Version)
	if err := server.Serve(ctx); err != nil {
		util.Event(logger, slog.LevelError, "fbmeasure.serve_failed", "error", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`fbmeasure - fixed small-packet RTT echo server

Usage:
  fbmeasure --listen <addr:port> [--log-level info] [--log-format text]
  fbmeasure --version
`)
}
