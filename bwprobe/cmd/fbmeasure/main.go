//go:build linux

package main

import (
	"flag"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/server"
)

func main() {
	port := flag.Int("port", 9876, "Listen port")
	recvWait := flag.Duration("recv-wait", 100*time.Millisecond, "Receive wait timeout")
	flag.Parse()

	cfg := server.Config{
		Port:     *port,
		RecvWait: *recvWait,
	}
	server.Run(cfg)
}
