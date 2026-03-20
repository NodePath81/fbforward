package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/NodePath81/fbforward/internal/fbmeasure"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/NodePath81/fbforward/internal/version"
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

	port := flag.Int("port", 9876, "Listen port")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text or json)")
	recvWait := flag.Duration("recv-wait", 100*time.Millisecond, "UDP receive idle window")
	tlsCertFile := flag.String("tls-cert-file", "", "TLS server certificate file")
	tlsKeyFile := flag.String("tls-key-file", "", "TLS server key file")
	tlsClientCAFile := flag.String("tls-client-ca-file", "", "CA bundle for validating client certificates")
	tlsRequireClientCert := flag.Bool("tls-require-client-cert", false, "Require and verify client certificates")
	showVersion := flag.Bool("version", false, "Print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	logger := util.ComponentLogger(util.NewLogger(*logLevel, *logFormat), "fbmeasure")
	if runtime.GOOS != "linux" {
		util.Event(logger, slog.LevelError, "fbmeasure.unsupported_os", "goos", runtime.GOOS)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := fbmeasure.NewServer(fbmeasure.Config{
		Port:           *port,
		UDPReceiveWait: *recvWait,
		Security: fbmeasure.ServerSecurityConfig{
			CertFile:          *tlsCertFile,
			KeyFile:           *tlsKeyFile,
			ClientCAFile:      *tlsClientCAFile,
			RequireClientCert: *tlsRequireClientCert,
		},
	}, logger)
	if err := srv.Start(ctx); err != nil {
		util.Event(logger, slog.LevelError, "fbmeasure.start_failed", "error", err)
		os.Exit(1)
	}
	util.Event(logger, slog.LevelInfo, "fbmeasure.ready", "port", srv.Port(), "version", version.Version)
	srv.Wait()
}

func printHelp() {
	fmt.Print(`fbmeasure - targeted measurement server

Usage:
  fbmeasure --port <port> [--log-level info] [--log-format text]
  fbmeasure --tls-cert-file server.crt --tls-key-file server.key [--tls-client-ca-file ca.crt --tls-require-client-cert]
  fbmeasure --version
`)
}
