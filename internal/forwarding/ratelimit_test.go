package forwarding

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func TestByteRateLimiterUsesBitsPerSecondAndSharedBudget(t *testing.T) {
	limiter := newByteRateLimiter(8000)
	if limiter == nil {
		t.Fatal("expected limiter")
	}
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("initial burst should be available")
	}
	if limiter.Try(64 * 1024) {
		t.Fatal("shared bucket should reject an over-budget packet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := limiter.Wait(ctx, 256); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("wait completed too quickly: %s", elapsed)
	}
}

func TestByteRateLimiterContextCancellation(t *testing.T) {
	limiter := newByteRateLimiter(1)
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("expected to consume initial burst")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Wait(ctx, 1024); err == nil {
		t.Fatal("expected cancelled wait")
	}
}

func TestByteRateLimiterBackendOverrideCannotWidenBasePolicy(t *testing.T) {
	limiter := newByteRateLimiter(16000)
	limiter.SetOverride(8000)
	limiter.mu.Lock()
	if limiter.rate != 1000 {
		limiter.mu.Unlock()
		t.Fatalf("expected 8000 bit/s effective rate, got %f bytes/s", limiter.rate)
	}
	limiter.mu.Unlock()

	limiter.SetOverride(32000)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.rate != 2000 {
		t.Fatalf("backend override widened base policy: got %f bytes/s", limiter.rate)
	}
}

func TestByteRateLimiterClearOverrideRestoresBaseAndWakesWaiter(t *testing.T) {
	limiter := newByteRateLimiter(0)
	limiter.SetOverride(1)
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("expected to consume initial burst")
	}

	done := make(chan error, 1)
	go func() { done <- limiter.Wait(context.Background(), 1) }()
	time.Sleep(10 * time.Millisecond)
	limiter.ClearOverride()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait after clear: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("clearing the limit did not wake the waiter")
	}
}

func TestByteRateLimiterClearOverrideCancelsLargeWait(t *testing.T) {
	limiter := newByteRateLimiter(0)
	limiter.SetOverride(1)
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("expected to consume initial burst")
	}
	done := make(chan error, 1)
	go func() { done <- limiter.Wait(context.Background(), 64*1024) }()
	time.Sleep(10 * time.Millisecond)
	limiter.ClearOverride()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("large wait after clear: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("large wait remained blocked after clear")
	}
}

func TestByteRateLimiterSetOverrideWakesWaiter(t *testing.T) {
	limiter := newByteRateLimiter(0)
	limiter.SetOverride(1)
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("expected to consume initial burst")
	}
	done := make(chan error, 1)
	go func() { done <- limiter.Wait(context.Background(), 1) }()
	time.Sleep(10 * time.Millisecond)
	limiter.SetOverride(16000)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait after set: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("set override did not wake waiter")
	}
}

func TestByteRateLimiterClearRestoresBaseRate(t *testing.T) {
	limiter := newByteRateLimiter(16000)
	limiter.SetOverride(8000)
	limiter.ClearOverride()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.rate != 2000 {
		t.Fatalf("clear did not restore base rate: %f", limiter.rate)
	}
}

func TestByteRateLimiterSameOverridePreservesBudget(t *testing.T) {
	limiter := newByteRateLimiter(16000)
	limiter.SetOverride(8000)
	limiter.mu.Lock()
	limiter.tokens = 123
	limiter.last = time.Now()
	before := limiter.tokens
	limiter.mu.Unlock()
	limiter.SetOverride(8000)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.tokens <= before || limiter.tokens >= limiter.burst {
		t.Fatalf("same override unexpectedly reset budget: before=%f after=%f burst=%f", before, limiter.tokens, limiter.burst)
	}
}

func TestByteRateLimiterCanBeEnabledAfterFlowCreation(t *testing.T) {
	limiter := newByteRateLimiter(0)
	if !limiter.Try(64 * 1024) {
		t.Fatal("disabled limiter should allow traffic")
	}
	limiter.SetOverride(8000)
	if limiter.Try(minRateLimitBurst) == false {
		t.Fatal("newly enabled limiter should have an initial burst")
	}
}

func TestTCPProxyWaitsForFlowRateBudget(t *testing.T) {
	limiter := newByteRateLimiter(8000)
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("expected to consume initial burst")
	}
	source := &scriptedRateConn{data: make([]byte, 256)}
	sink := &scriptedRateConn{}
	finished := make(chan struct{})
	start := time.Now()
	go func() {
		copyTCP(context.Background(), sink, source, limiter, make([]byte, tcpBufferSize), nil)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("proxy did not finish")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("TCP proxy did not wait for rate budget: %s", elapsed)
	}
	if len(sink.writes) != 256 {
		t.Fatalf("unexpected forwarded bytes: %d", len(sink.writes))
	}
}

func TestUDPOverBudgetPacketIsDropped(t *testing.T) {
	limiter := newByteRateLimiter(8000)
	if limiter == nil {
		t.Fatal("expected limiter")
	}
	if !limiter.Try(minRateLimitBurst) {
		t.Fatal("expected to consume initial burst")
	}
	recorder := &rateDropRecorder{}
	mapping := &udpMapping{
		parent:      &UDPListener{dropRecorder: recorder},
		rateLimiter: limiter,
	}
	if err := mapping.forwardToUpstream(make([]byte, minRateLimitBurst)); err != errUDPRateLimited {
		t.Fatalf("expected rate-limit error, got %v", err)
	}
	if recorder.bytes != minRateLimitBurst || recorder.protocol != "udp" {
		t.Fatalf("unexpected drop telemetry: %+v", recorder)
	}
}

func TestUDPWithinBudgetReachesUpstream(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	upstream, err := net.DialUDP("udp", nil, sink.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	mapping := &udpMapping{
		parent:       &UDPListener{},
		upstreamConn: upstream,
		rateLimiter:  newByteRateLimiter(8000),
	}
	payload := []byte("within-budget")
	if err := mapping.forwardToUpstream(payload); err != nil {
		t.Fatalf("forwardToUpstream: %v", err)
	}
	_ = sink.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	n, _, err := sink.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read upstream packet: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("unexpected upstream payload %q", buf[:n])
	}
}

func TestUDPFlowControlLimitAppliesToNextPacket(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	upstream, err := net.DialUDP("udp", nil, sink.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	recorder := &rateDropRecorder{}
	registry := flow.NewRegistry()
	mapping := &udpMapping{
		parent:       &UDPListener{dropRecorder: recorder},
		upstreamConn: upstream,
		rateLimiter:  newByteRateLimiter(0),
		done:         make(chan struct{}),
	}
	mapping.id, _ = flow.NewID()
	registry.Register(flow.Meta{ID: mapping.id, Protocol: flow.ProtocolUDP}, mapping.close)
	registry.SetControls(mapping.id, flow.Controls{
		SetLimit:   mapping.setRateLimit,
		ClearLimit: mapping.clearRateLimit,
	})
	if !registry.SetLimit(mapping.id, 1) {
		t.Fatal("expected UDP limit control to apply")
	}
	if !mapping.rateLimiter.Try(minRateLimitBurst) {
		t.Fatal("expected to consume initial burst")
	}
	if err := mapping.forwardToUpstream([]byte("next-packet")); err != errUDPRateLimited {
		t.Fatalf("expected next packet to be dropped, got %v", err)
	}
	if recorder.bytes == 0 {
		t.Fatal("expected UDP drop telemetry")
	}
}

type rateDropRecorder struct {
	protocol string
	bytes    int
}

func (r *rateDropRecorder) RecordRateLimitDrop(protocol string, bytes uint64) {
	r.protocol = protocol
	r.bytes += int(bytes)
}

type scriptedRateConn struct {
	data   []byte
	read   bool
	writes []byte
}

func (c *scriptedRateConn) Read(dst []byte) (int, error) {
	if c.read {
		return 0, io.EOF
	}
	c.read = true
	copy(dst, c.data)
	return len(c.data), nil
}

func (c *scriptedRateConn) Write(src []byte) (int, error) {
	c.writes = append(c.writes, src...)
	return len(src), nil
}

func (c *scriptedRateConn) Close() error                     { return nil }
func (c *scriptedRateConn) LocalAddr() net.Addr              { return nil }
func (c *scriptedRateConn) RemoteAddr() net.Addr             { return nil }
func (c *scriptedRateConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptedRateConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptedRateConn) SetWriteDeadline(time.Time) error { return nil }
