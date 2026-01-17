package metrics

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
)

// PingTCP issues a TCP ping with a default timeout.
func PingTCP(target string, port int) (time.Duration, error) {
	return PingTCPWithTimeout(target, port, time.Second)
}

// PingTCPWithTimeout issues a TCP ping with a caller-provided timeout.
func PingTCPWithTimeout(target string, port int, timeout time.Duration) (time.Duration, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", target, port), timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))
	start := time.Now()
	if _, err := conn.Write([]byte(protocol.TCPPingHeader)); err != nil {
		return 0, err
	}

	resp := make([]byte, len(protocol.TCPPongHeader))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return 0, err
	}
	if string(resp) != protocol.TCPPongHeader {
		return 0, errors.New("unexpected tcp pong")
	}
	return time.Since(start), nil
}

// PingUDP issues a UDP ping with a default timeout.
func PingUDP(target string, port int) (time.Duration, error) {
	return PingUDPWithTimeout(target, port, time.Second)
}

// PingUDPWithTimeout issues a UDP ping with a caller-provided timeout.
func PingUDPWithTimeout(target string, port int, timeout time.Duration) (time.Duration, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", target, port))
	if err != nil {
		return 0, err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	payload := make([]byte, 1+8)
	payload[0] = protocol.UDPTypePing
	binary.BigEndian.PutUint64(payload[1:], uint64(time.Now().UnixNano()))

	_ = conn.SetDeadline(time.Now().Add(timeout))
	start := time.Now()
	if _, err := conn.Write(payload); err != nil {
		return 0, err
	}

	resp := make([]byte, 1+8)
	n, err := conn.Read(resp)
	if err != nil {
		return 0, err
	}
	if n < 1 || resp[0] != protocol.UDPTypePong {
		return 0, errors.New("unexpected udp pong")
	}

	return time.Since(start), nil
}
