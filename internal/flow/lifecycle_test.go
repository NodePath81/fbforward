package flow

import (
	"net/netip"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

type recordingObserver struct {
	mu         sync.Mutex
	opens      []Meta
	updates    []Counters
	closes     []Summary
	rejections []Rejection
}

func (o *recordingObserver) Open(meta Meta) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opens = append(o.opens, meta)
}

func (o *recordingObserver) Update(_ ID, counters Counters) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.updates = append(o.updates, counters)
}

func (o *recordingObserver) Close(summary Summary) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closes = append(o.closes, summary)
}

func (o *recordingObserver) Reject(rejection Rejection) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rejections = append(o.rejections, rejection)
}

func newTestMeta(t *testing.T, upstream string) Meta {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	return Meta{
		ID:         id,
		Protocol:   ProtocolTCP,
		ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"),
		Listener:   "127.0.0.1:9000",
		Upstream:   upstream,
		StartedAt:  time.Unix(100, 0).UTC(),
	}
}

func TestLifecyclePublishesCumulativeUpdatesAndOneClose(t *testing.T) {
	observer := &recordingObserver{}
	meta := newTestMeta(t, "primary")
	lifecycle := NewLifecycle(meta, observer, nil, nil)
	lifecycle.Open()
	lifecycle.Open()
	lifecycle.AddAt(time.Unix(101, 0), 10, 2, 1, 1)
	lifecycle.AddAt(time.Unix(102, 0), 5, 3, 1, 1)
	lifecycle.Close("eof")
	lifecycle.Close("duplicate")

	if len(observer.opens) != 1 {
		t.Fatalf("expected one Open, got %d", len(observer.opens))
	}
	if len(observer.updates) != 2 || observer.updates[1].BytesUp != 15 || observer.updates[1].BytesDown != 5 {
		t.Fatalf("unexpected cumulative updates: %+v", observer.updates)
	}
	if len(observer.closes) != 1 {
		t.Fatalf("expected one Close, got %d", len(observer.closes))
	}
	summary := observer.closes[0]
	if summary.BytesUp != 15 || summary.BytesDown != 5 || summary.CloseReason != "eof" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if !summary.LastActivity.Equal(time.Unix(102, 0).UTC()) {
		t.Fatalf("unexpected last activity %s", summary.LastActivity)
	}
}

func TestLifecycleConcurrentCloseIsIdempotent(t *testing.T) {
	observer := &recordingObserver{}
	meta := newTestMeta(t, "primary")
	lifecycle := NewLifecycle(meta, observer, nil, nil)
	lifecycle.Open()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lifecycle.Close("context_done")
		}()
	}
	wg.Wait()
	if len(observer.closes) != 1 {
		t.Fatalf("expected one concurrent Close, got %d", len(observer.closes))
	}
}

func TestRegistryClosesMatchingFlowsOutsideRegistryLock(t *testing.T) {
	registry := NewRegistry()
	first := newTestMeta(t, "primary")
	second := newTestMeta(t, "backup")
	var mu sync.Mutex
	closed := make([]ID, 0, 2)
	registry.Register(first, func() {
		mu.Lock()
		closed = append(closed, first.ID)
		mu.Unlock()
		registry.Unregister(first.ID)
	})
	registry.Register(second, func() {
		mu.Lock()
		closed = append(closed, second.ID)
		mu.Unlock()
	})

	registry.CloseByUpstream("primary")
	mu.Lock()
	defer mu.Unlock()
	if len(closed) != 1 || closed[0] != first.ID {
		t.Fatalf("unexpected matching closes: %v", closed)
	}
}

func TestRegistryDispatchesFlowControlsOutsideRegistryLock(t *testing.T) {
	registry := NewRegistry()
	meta := newTestMeta(t, "primary")
	registry.Register(meta, nil)

	var mu sync.Mutex
	var calls []string
	registry.SetControls(meta.ID, Controls{
		Block: func() bool {
			mu.Lock()
			calls = append(calls, "block")
			mu.Unlock()
			registry.Unregister(meta.ID)
			return true
		},
		SetLimit: func(rate uint64) bool {
			mu.Lock()
			calls = append(calls, "limit:"+strconv.FormatUint(rate, 10))
			mu.Unlock()
			return true
		},
		ClearLimit: func() bool {
			mu.Lock()
			calls = append(calls, "clear")
			mu.Unlock()
			return true
		},
	})

	if !registry.SetLimit(meta.ID, 1000) || !registry.ClearLimit(meta.ID) || !registry.Block(meta.ID) {
		t.Fatal("expected all controls to dispatch")
	}
	if registry.SetLimit(meta.ID, 2000) {
		t.Fatal("expected controls to be unavailable after unregister")
	}

	mu.Lock()
	defer mu.Unlock()
	if got, want := calls, []string{"limit:1000", "clear", "block"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected control calls: got %v want %v", got, want)
	}
}

func TestRegistryReturnsControlApplicationResult(t *testing.T) {
	registry := NewRegistry()
	meta := newTestMeta(t, "primary")
	registry.Register(meta, nil)
	registry.SetControls(meta.ID, Controls{
		SetLimit: func(uint64) bool { return false },
	})
	if registry.SetLimit(meta.ID, 1000) {
		t.Fatal("expected failed control application to be reported")
	}
}
