package flowcontext

import (
	"context"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func testMeta(id flow.ID, protocol string) flow.Meta {
	return flow.Meta{
		ID:         id,
		Protocol:   protocol,
		ClientAddr: mustAddrPort("203.0.113.10:45678"),
		Listener:   "0.0.0.0:443",
		Upstream:   "primary",
		StartedAt:  time.Now().UTC(),
	}
}

func mustAddrPort(value string) netip.AddrPort {
	address, err := netip.ParseAddrPort(value)
	if err != nil {
		panic(err)
	}
	return address
}

func testTuple(protocol string) flow.BackendTuple {
	return flow.BackendTuple{
		Protocol:   protocol,
		BackendKey: "primary@192.0.2.10:443",
		LocalAddr:  mustAddrPort("10.0.0.1:43122"),
		RemoteAddr: mustAddrPort("192.0.2.10:443"),
	}
}

func TestRegistryOpenBindResolve(t *testing.T) {
	r := NewRegistry(Options{CleanupInterval: time.Millisecond})
	defer r.Shutdown()
	meta := testMeta("f1", flow.ProtocolTCP)
	r.Open(meta)
	if err := r.Bind(meta.ID, testTuple(flow.ProtocolTCP)); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	got, ok := r.Resolve(context.Background(), testTuple(flow.ProtocolTCP), 0)
	if !ok {
		t.Fatal("expected active context")
	}
	if got.FlowID != meta.ID || got.State != StateActive || got.BackendKey == "" {
		t.Fatalf("unexpected context: %+v", got)
	}
}

func TestRegistryTupleReuseDoesNotReturnOldFlow(t *testing.T) {
	r := NewRegistry(Options{CleanupInterval: time.Millisecond, GracePeriod: time.Second})
	defer r.Shutdown()
	tuple := testTuple(flow.ProtocolTCP)
	old := testMeta("old", flow.ProtocolTCP)
	newMeta := testMeta("new", flow.ProtocolTCP)
	r.Open(old)
	if err := r.Bind(old.ID, tuple); err != nil {
		t.Fatal(err)
	}
	r.Open(newMeta)
	if err := r.Bind(newMeta.ID, tuple); err != nil {
		t.Fatal(err)
	}
	r.Close(flow.Summary{Meta: old, EndedAt: time.Now()})
	got, ok := r.Resolve(context.Background(), tuple, 0)
	if !ok || got.FlowID != newMeta.ID {
		t.Fatalf("tuple resolved to %+v, ok=%v", got, ok)
	}
}

func TestRegistryCloseGraceAndExpiry(t *testing.T) {
	r := NewRegistry(Options{CleanupInterval: time.Millisecond, GracePeriod: 8 * time.Millisecond})
	defer r.Shutdown()
	meta := testMeta("f1", flow.ProtocolTCP)
	tuple := testTuple(flow.ProtocolTCP)
	r.Open(meta)
	if err := r.Bind(meta.ID, tuple); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	r.Close(flow.Summary{Meta: meta, EndedAt: ended})
	if got, ok := r.Resolve(context.Background(), tuple, 0); !ok || got.State != StateClosed {
		t.Fatalf("expected closed grace context, got %+v, ok=%v", got, ok)
	}
	time.Sleep(30 * time.Millisecond)
	if _, ok := r.Resolve(context.Background(), tuple, 0); ok {
		t.Fatal("expected expired context")
	}
}

func TestRegistryWaitsForBind(t *testing.T) {
	r := NewRegistry(Options{CleanupInterval: time.Millisecond, ResolveTimeout: 200 * time.Millisecond})
	defer r.Shutdown()
	meta := testMeta("f1", flow.ProtocolTCP)
	tuple := testTuple(flow.ProtocolTCP)
	r.Open(meta)
	result := make(chan Context, 1)
	go func() {
		got, ok := r.Resolve(context.Background(), tuple, 100*time.Millisecond)
		if ok {
			result <- got
			return
		}
		result <- Context{}
	}()
	time.Sleep(10 * time.Millisecond)
	if err := r.Bind(meta.ID, tuple); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.FlowID != meta.ID {
			t.Fatalf("unexpected waited context: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Resolve did not wake after Bind")
	}
}

func TestRegistrySeparatesBackendAndProtocol(t *testing.T) {
	r := NewRegistry(Options{CleanupInterval: time.Millisecond})
	defer r.Shutdown()
	meta := testMeta("f1", flow.ProtocolTCP)
	tuple := testTuple(flow.ProtocolTCP)
	r.Open(meta)
	if err := r.Bind(meta.ID, tuple); err != nil {
		t.Fatal(err)
	}
	otherBackend := tuple
	otherBackend.BackendKey = "secondary@192.0.2.10:443"
	udp := tuple
	udp.Protocol = flow.ProtocolUDP
	if _, ok := r.Resolve(context.Background(), otherBackend, 0); ok {
		t.Fatal("different backend must not resolve")
	}
	if _, ok := r.Resolve(context.Background(), udp, 0); ok {
		t.Fatal("different protocol must not resolve")
	}
}

func TestRegistryCapacityAndShutdown(t *testing.T) {
	r := NewRegistry(Options{MaxEntries: 1, CleanupInterval: time.Millisecond})
	meta := testMeta("f1", flow.ProtocolTCP)
	r.Open(meta)
	r.Open(testMeta("f2", flow.ProtocolTCP))
	stats := r.Stats()
	if stats.Active != 1 || stats.CapacityRejects != 1 {
		t.Fatalf("unexpected capacity stats: %+v", stats)
	}
	if err := r.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := r.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if !r.IsClosed() {
		t.Fatal("registry should be closed")
	}
}

func TestRegistryConcurrentLifecycleAndResolve(t *testing.T) {
	r := NewRegistry(Options{MaxEntries: 256, CleanupInterval: time.Millisecond, GracePeriod: time.Second})
	defer r.Shutdown()
	tuple := testTuple(flow.ProtocolTCP)
	const count = 64
	done := make(chan struct{})
	for i := 0; i < count; i++ {
		id := flow.ID("f-" + strconv.Itoa(i))
		go func(id flow.ID) {
			meta := testMeta(id, flow.ProtocolTCP)
			r.Open(meta)
			_ = r.Bind(id, tuple)
			_, _ = r.Resolve(context.Background(), tuple, time.Millisecond)
			r.Close(flow.Summary{Meta: meta, EndedAt: time.Now()})
			done <- struct{}{}
		}(id)
	}
	for i := 0; i < count; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("concurrent lifecycle did not complete")
		}
	}
}
