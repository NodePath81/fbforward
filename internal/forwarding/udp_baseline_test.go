package forwarding

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
)

func TestUDPConcurrentFirstPacketsCreateOneMapping(t *testing.T) {
	sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	picker := &fakePicker{selected: Upstream{Tag: "primary", Addr: netip.MustParseAddr("127.0.0.1")}}
	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindAddr: "127.0.0.1", BindPort: sink.LocalAddr().(*net.UDPAddr).Port},
		picker:   picker,
		policy:   allowedPolicy(),
		timeout:  time.Hour,
		sem:      make(chan struct{}, 8),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: udpMaxMappingsPerIP,
	}

	clientAddr := &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 12345}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const packets = 16
	var wg sync.WaitGroup
	for i := 0; i < packets; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			listener.handlePacket(ctx, clientAddr, []byte("packet"))
		}()
	}
	wg.Wait()

	listener.mu.Lock()
	mapping := listener.mappings[clientAddr.String()]
	mappingCount := len(listener.mappings)
	listener.mu.Unlock()
	if mappingCount != 1 || mapping == nil {
		t.Fatalf("mapping count=%d mapping=%v, want one mapping", mappingCount, mapping != nil)
	}
	if got := picker.calls(); got != 1 {
		t.Fatalf("upstream picker calls=%d, want 1", got)
	}
	mapping.closeWithReason("test")
}

func BenchmarkUDPMappingHit(b *testing.B) {
	for _, size := range []int{64, 1200, 1500, 65507} {
		b.Run(udpPacketSizeName(size), func(b *testing.B) {
			mapping, stop := benchmarkUDPMapping(b)
			defer stop()
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := mapping.forwardToUpstream(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func udpPacketSizeName(size int) string {
	switch size {
	case 64:
		return "64B"
	case 1200:
		return "1200B"
	case 1500:
		return "1500B"
	case 65507:
		return "max"
	default:
		return "unknown"
	}
}

func BenchmarkUDPMappingHitParallel(b *testing.B) {
	mapping, stop := benchmarkUDPMapping(b)
	defer stop()
	payload := make([]byte, 1200)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := mapping.forwardToUpstream(payload); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkUDPMappingLookup(b *testing.B) {
	key := "192.0.2.10:12345"
	listener := &UDPListener{
		mappings: map[string]*udpMapping{key: {}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if listener.lookupMapping(key) == nil {
			b.Fatal("mapping lookup missed")
		}
	}
}

func BenchmarkUDPMappingLookupParallel(b *testing.B) {
	key := "192.0.2.10:12345"
	listener := &UDPListener{
		mappings: map[string]*udpMapping{key: {}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if listener.lookupMapping(key) == nil {
				b.Fatal("mapping lookup missed")
			}
		}
	})
}

func BenchmarkUDPMappingCreate(b *testing.B) {
	sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatal(err)
	}
	defer sink.Close()
	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindAddr: "127.0.0.1", BindPort: sink.LocalAddr().(*net.UDPAddr).Port},
		picker:   &fakePicker{selected: Upstream{Tag: "primary", Addr: netip.MustParseAddr("127.0.0.1")}},
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
	}
	clientAddr := &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 12345}
	candidate, err := newCandidateMeta(flow.ProtocolUDP, clientAddr.String(), listener.listenAddr(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		listener.sem <- struct{}{}
		mapping, err := listener.buildMapping(clientAddr, candidate, Decision{})
		if err != nil {
			b.Fatal(err)
		}
		mapping.closeWithReason("benchmark")
	}
}

func benchmarkUDPMapping(b *testing.B) (*udpMapping, func()) {
	b.Helper()
	sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatal(err)
	}
	upstream, err := net.DialUDP("udp4", nil, sink.LocalAddr().(*net.UDPAddr))
	if err != nil {
		sink.Close()
		b.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			if _, _, err := sink.ReadFromUDP(buf); err != nil {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	mapping := &udpMapping{
		parent:       &UDPListener{},
		upstreamConn: upstream,
		activityCh:   make(chan struct{}, 1),
	}
	cleanup := func() {
		close(stop)
		_ = upstream.Close()
		_ = sink.Close()
		<-done
	}
	return mapping, cleanup
}
