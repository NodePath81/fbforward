package fbmeasure

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

type stubAddr string

func (a stubAddr) Network() string { return "stub" }
func (a stubAddr) String() string  { return string(a) }

type stubConn struct {
	mu        sync.Mutex
	writes    [][]byte
	deadlines []time.Time
	closed    bool
}

func (c *stubConn) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (c *stubConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	buf := make([]byte, len(p))
	copy(buf, p)
	c.writes = append(c.writes, buf)
	return len(p), nil
}

func (c *stubConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *stubConn) LocalAddr() net.Addr  { return stubAddr("local") }
func (c *stubConn) RemoteAddr() net.Addr { return stubAddr("remote") }

func (c *stubConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadlines = append(c.deadlines, t)
	return nil
}

func (c *stubConn) SetReadDeadline(t time.Time) error {
	return c.SetDeadline(t)
}

func (c *stubConn) SetWriteDeadline(t time.Time) error {
	return c.SetDeadline(t)
}

func TestWithLockedCallSideEffectErrorDoesNotPanic(t *testing.T) {
	conn := &stubConn{}
	client := &Client{
		conn:   conn,
		nextID: 1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	sideEffectErr := errors.New("side effect failed")
	err := client.withLockedCall(ctx, opPingUDP, pingUDPRequest{
		TestID:    "test",
		AuthKey:   "key",
		Count:     1,
		TimeoutMs: 100,
	}, func() error {
		return sideEffectErr
	}, nil)
	if !errors.Is(err, sideEffectErr) {
		t.Fatalf("withLockedCall err = %v, want %v", err, sideEffectErr)
	}
	if client.conn != nil {
		t.Fatal("withLockedCall should clear client connection after sideEffect error")
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if !conn.closed {
		t.Fatal("withLockedCall should close the active connection on sideEffect error")
	}
	if len(conn.writes) == 0 {
		t.Fatal("withLockedCall should write the control request before running the side effect")
	}
	if len(conn.deadlines) < 2 {
		t.Fatalf("withLockedCall should set and then reset the deadline, saw %d calls", len(conn.deadlines))
	}
	if !conn.deadlines[len(conn.deadlines)-1].IsZero() {
		t.Fatalf("withLockedCall should reset the deadline to zero, got %v", conn.deadlines[len(conn.deadlines)-1])
	}
}

func TestPingUDPFailureReturnsErrorWithoutPanic(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer tcpListener.Close()

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := tcpListener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()

		var req controlRequest
		if readErr := readControlMessage(conn, &req); readErr != nil {
			serverDone <- readErr
			return
		}
		if req.Op != opPingUDP {
			serverDone <- errors.New("unexpected control op")
			return
		}

		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
		serverDone <- nil
	}()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpListener.Addr().(*net.TCPAddr).Port))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	client, err := Dial(ctx, addr, ClientSecurityConfig{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	_, err = client.PingUDP(ctx, 1)
	if err == nil {
		t.Fatal("PingUDP should fail when the UDP side does not respond")
	}
	if client.conn != nil {
		t.Fatal("PingUDP failure should invalidate the client control connection")
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("fake control server: %v", err)
	}
}
