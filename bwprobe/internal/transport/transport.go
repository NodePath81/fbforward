package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
)

// DialReverseTCP opens a reverse TCP data connection and sends the RECV header.
func DialReverseTCP(target string, port int, sessionID string) (*net.TCPConn, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", target, port), 3*time.Second)
	if err != nil {
		return nil, err
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("connection is not TCP")
	}
	if _, err := tcpConn.Write([]byte(protocol.TCPReverseHeader)); err != nil {
		_ = tcpConn.Close()
		return nil, err
	}
	if sessionID != "" {
		sessionBytes := []byte(sessionID)
		if len(sessionBytes) > 65535 {
			_ = tcpConn.Close()
			return nil, errors.New("session id too long")
		}
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(sessionBytes)))
		if _, err := tcpConn.Write(lenBuf); err != nil {
			_ = tcpConn.Close()
			return nil, err
		}
		if _, err := tcpConn.Write(sessionBytes); err != nil {
			_ = tcpConn.Close()
			return nil, err
		}
	}
	return tcpConn, nil
}

// ListenReverseUDP opens a UDP socket for reverse data reception.
func ListenReverseUDP() (*net.UDPConn, int, error) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, 0, err
	}
	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		_ = conn.Close()
		return nil, 0, errors.New("udp local addr not found")
	}
	_ = conn.SetReadBuffer(protocol.UDPMaxChunkSize * 4)
	return conn, localAddr.Port, nil
}

// SendUDPHello sends a UDP ping so the server can validate the endpoint.
func SendUDPHello(conn *net.UDPConn, target string, port int) error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", target, port))
	if err != nil {
		return err
	}
	payload := make([]byte, 1+8)
	payload[0] = protocol.UDPTypePing
	var lastErr error
	for i := 0; i < 3; i++ {
		binary.BigEndian.PutUint64(payload[1:], uint64(time.Now().UnixNano()))
		if _, err = conn.WriteToUDP(payload, addr); err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return lastErr
}
