package flow

import (
	"sync"
	"time"
)

// Lifecycle owns the mutable counters and the exactly-once close transition
// for one Flow. Observer callbacks are serialized with lifecycle transitions so
// an Update cannot be delivered after Close.
type Lifecycle struct {
	mu       sync.Mutex
	meta     Meta
	observer Observer
	registry *Registry
	closeFn  func()

	opened  bool
	closed  bool
	counter Counters
}

func NewLifecycle(meta Meta, observer Observer, registry *Registry, closeFn func()) *Lifecycle {
	if observer == nil {
		observer = NopObserver{}
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now().UTC()
	} else {
		meta.StartedAt = meta.StartedAt.UTC()
	}
	return &Lifecycle{
		meta:     meta,
		observer: observer,
		registry: registry,
		closeFn:  closeFn,
	}
}

// Open publishes the immutable metadata exactly once.
func (l *Lifecycle) Open() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opened || l.closed {
		return
	}
	l.opened = true
	if l.registry != nil {
		l.registry.Register(l.meta, l.closeFn)
	}
	l.observer.Open(l.meta)
}

// Add records a cumulative counter update using the current UTC time.
func (l *Lifecycle) Add(bytesUp, bytesDown, segmentsUp, segmentsDown uint64) {
	l.AddAt(time.Now().UTC(), bytesUp, bytesDown, segmentsUp, segmentsDown)
}

// AddAt is the deterministic form used by tests and by callers that already
// have an activity timestamp.
func (l *Lifecycle) AddAt(now time.Time, bytesUp, bytesDown, segmentsUp, segmentsDown uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.opened || l.closed {
		return
	}
	l.counter.BytesUp += bytesUp
	l.counter.BytesDown += bytesDown
	l.counter.SegmentsUp += segmentsUp
	l.counter.SegmentsDown += segmentsDown
	l.counter.LastActivity = now.UTC()
	l.observer.Update(l.meta.ID, l.counter)
}

// Snapshot returns the latest cumulative counters without changing lifecycle
// state.
func (l *Lifecycle) Snapshot() Counters {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counter
}

// Close publishes one final summary. Repeated and concurrent calls are safe.
func (l *Lifecycle) Close(reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	ended := time.Now().UTC()
	lastActivity := l.counter.LastActivity
	if lastActivity.IsZero() {
		lastActivity = l.meta.StartedAt
	}
	if l.registry != nil {
		l.registry.Unregister(l.meta.ID)
	}
	l.observer.Close(Summary{
		Meta:         l.meta,
		EndedAt:      ended,
		LastActivity: lastActivity,
		BytesUp:      l.counter.BytesUp,
		BytesDown:    l.counter.BytesDown,
		CloseReason:  reason,
	})
}

// Registry tracks the close functions needed by runtime-level upstream and
// shutdown operations. It is deliberately separate from StatusStore so the
// status projection remains read-only state.
type Registry struct {
	mu      sync.Mutex
	entries map[ID]registryEntry
}

// Controls are the small set of operations that an external, authorized
// controller may apply to an active Flow. The callbacks are owned by the
// data-plane connection; Registry only stores and dispatches them.
type Controls struct {
	Block      func()
	SetLimit   func(uint64)
	ClearLimit func()
}

type registryEntry struct {
	meta     Meta
	closeFn  func()
	controls Controls
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[ID]registryEntry)}
}

func (r *Registry) Register(meta Meta, closeFn func()) {
	if r == nil || meta.ID == "" {
		return
	}
	r.mu.Lock()
	r.entries[meta.ID] = registryEntry{meta: meta, closeFn: closeFn}
	r.mu.Unlock()
}

func (r *Registry) Unregister(id ID) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.entries, id)
	r.mu.Unlock()
}

// SetControls attaches data-plane controls to an already-open Flow. It is
// intentionally separate from Register so Lifecycle and forwarding can keep
// their existing construction APIs.
func (r *Registry) SetControls(id ID, controls Controls) bool {
	if r == nil || id == "" {
		return false
	}
	r.mu.Lock()
	entry, ok := r.entries[id]
	if ok {
		entry.controls = controls
		r.entries[id] = entry
	}
	r.mu.Unlock()
	return ok
}

func (r *Registry) Block(id ID) bool {
	return r.withControl(id, func(controls Controls) func() { return controls.Block })
}

func (r *Registry) SetLimit(id ID, rateBPS uint64) bool {
	return r.withControl(id, func(controls Controls) func() {
		if controls.SetLimit == nil {
			return nil
		}
		return func() { controls.SetLimit(rateBPS) }
	})
}

func (r *Registry) ClearLimit(id ID) bool {
	return r.withControl(id, func(controls Controls) func() { return controls.ClearLimit })
}

func (r *Registry) withControl(id ID, pick func(Controls) func()) bool {
	if r == nil || id == "" {
		return false
	}
	r.mu.Lock()
	entry, ok := r.entries[id]
	var callback func()
	if ok {
		callback = pick(entry.controls)
	}
	r.mu.Unlock()
	if callback == nil {
		return false
	}
	callback()
	return true
}

func (r *Registry) CloseByUpstream(upstream string) {
	if r == nil {
		return
	}
	r.closeMatching(func(entry registryEntry) bool { return entry.meta.Upstream == upstream })
}

func (r *Registry) CloseAll() {
	if r == nil {
		return
	}
	r.closeMatching(func(registryEntry) bool { return true })
}

func (r *Registry) closeMatching(match func(registryEntry) bool) {
	r.mu.Lock()
	closers := make([]func(), 0, len(r.entries))
	for _, entry := range r.entries {
		if match(entry) && entry.closeFn != nil {
			closers = append(closers, entry.closeFn)
		}
	}
	r.mu.Unlock()
	for _, closeFn := range closers {
		closeFn()
	}
}
