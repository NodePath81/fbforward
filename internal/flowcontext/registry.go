package flowcontext

import (
	stdcontext "context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

const (
	StateActive = "active"
	StateClosed = "closed"
)

var (
	ErrClosed           = errors.New("flow context registry is closed")
	ErrInvalidTuple     = errors.New("invalid backend tuple")
	ErrCapacityExceeded = errors.New("flow context registry capacity exceeded")
	ErrFlowNotFound     = errors.New("flow context flow not found")
)

type Options struct {
	MaxEntries      int
	ResolveTimeout  time.Duration
	GracePeriod     time.Duration
	CleanupInterval time.Duration
}

func DefaultOptions() Options {
	return Options{
		MaxEntries:      100000,
		ResolveTimeout:  5 * time.Second,
		GracePeriod:     30 * time.Second,
		CleanupInterval: time.Second,
	}
}

func (o Options) normalized() Options {
	d := DefaultOptions()
	if o.MaxEntries > 0 {
		d.MaxEntries = o.MaxEntries
	}
	if o.ResolveTimeout > 0 {
		d.ResolveTimeout = o.ResolveTimeout
	}
	if o.GracePeriod > 0 {
		d.GracePeriod = o.GracePeriod
	}
	if o.CleanupInterval > 0 {
		d.CleanupInterval = o.CleanupInterval
	}
	return d
}

type Context struct {
	FlowID       flow.ID
	Protocol     string
	ClientAddr   string
	Listener     string
	Route        string
	Upstream     string
	BackendKey   string
	BackendTuple flow.BackendTuple
	CreatedAt    time.Time
	EndedAt      time.Time
	ResolveUntil time.Time
	State        string
	Generation   uint64
	LastActivity time.Time
	BytesUp      uint64
	BytesDown    uint64
}

type Stats struct {
	Active          int
	Closed          int
	BoundTuples     int
	Capacity        int
	CapacityRejects uint64
}

type entry struct {
	context  Context
	hasTuple bool
}

type Registry struct {
	mu        sync.RWMutex
	entries   map[flow.ID]*entry
	tuples    map[string]flow.ID
	options   Options
	updates   chan struct{}
	stop      chan struct{}
	stopped   chan struct{}
	closed    bool
	rejects   atomic.Uint64
	closeOnce sync.Once
}

func NewRegistry(options Options) *Registry {
	options = options.normalized()
	r := &Registry{
		entries: make(map[flow.ID]*entry),
		tuples:  make(map[string]flow.ID),
		options: options,
		updates: make(chan struct{}),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go r.cleanupLoop()
	return r
}

func (r *Registry) Open(meta flow.Meta) {
	if r == nil || meta.ID == "" {
		return
	}
	now := meta.StartedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.cleanupExpiredLocked(time.Now().UTC())
	if _, exists := r.entries[meta.ID]; exists {
		return
	}
	if len(r.entries) >= r.options.MaxEntries {
		r.rejects.Add(1)
		return
	}
	r.entries[meta.ID] = &entry{context: Context{
		FlowID: meta.ID, Protocol: meta.Protocol, ClientAddr: meta.ClientAddr.String(),
		Listener: meta.Listener, Route: meta.Route, Upstream: meta.Upstream,
		CreatedAt: now.UTC(), LastActivity: now.UTC(), State: StateActive,
	}}
	r.signalLocked()
}

func (r *Registry) Update(id flow.ID, counters flow.Counters) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if current := r.entries[id]; current != nil {
		if !counters.LastActivity.IsZero() {
			current.context.LastActivity = counters.LastActivity.UTC()
		}
		current.context.BytesUp = counters.BytesUp
		current.context.BytesDown = counters.BytesDown
	}
	r.mu.Unlock()
}

func (r *Registry) Close(summary flow.Summary) {
	if r == nil || summary.ID == "" {
		return
	}
	ended := summary.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.entries[summary.ID]
	if current == nil {
		return
	}
	current.context.State = StateClosed
	current.context.EndedAt = ended.UTC()
	current.context.ResolveUntil = ended.UTC().Add(r.options.GracePeriod)
	if !summary.LastActivity.IsZero() {
		current.context.LastActivity = summary.LastActivity.UTC()
	}
	current.context.BytesUp = summary.BytesUp
	current.context.BytesDown = summary.BytesDown
	r.signalLocked()
}

func (r *Registry) Reject(flow.Rejection) {}

func (r *Registry) Bind(id flow.ID, tuple flow.BackendTuple) error {
	if r == nil {
		return ErrClosed
	}
	key, err := tupleKey(tuple)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	current := r.entries[id]
	if current == nil || current.context.State != StateActive {
		return ErrFlowNotFound
	}
	r.cleanupExpiredLocked(time.Now().UTC())
	if previous, ok := r.tuples[key]; ok && previous != id {
		if old := r.entries[previous]; old != nil {
			oldKey, oldErr := tupleKey(old.context.BackendTuple)
			if old.hasTuple && oldErr == nil && oldKey == key {
				old.hasTuple = false
				old.context.BackendTuple = flow.BackendTuple{}
				old.context.BackendKey = ""
			}
		}
	}
	if current.hasTuple {
		if oldKey, oldOK := tupleKey(current.context.BackendTuple); oldOK == nil {
			if r.tuples[oldKey] == id {
				delete(r.tuples, oldKey)
			}
		}
	}
	current.context.Generation++
	current.context.BackendTuple = tuple
	current.context.BackendKey = tuple.BackendKey
	current.hasTuple = true
	r.tuples[key] = id
	r.signalLocked()
	return nil
}

func (r *Registry) Resolve(ctx stdcontext.Context, tuple flow.BackendTuple, wait time.Duration) (Context, bool) {
	if r == nil {
		return Context{}, false
	}
	key, err := tupleKey(tuple)
	if err != nil {
		return Context{}, false
	}
	if ctx == nil {
		ctx = stdcontext.Background()
	}
	if wait < 0 {
		wait = 0
	}
	if wait > r.options.ResolveTimeout {
		wait = r.options.ResolveTimeout
	}
	deadline := time.Now().Add(wait)
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return Context{}, false
		}
		r.cleanupExpiredLocked(time.Now().UTC())
		now := time.Now().UTC()
		if id, ok := r.tuples[key]; ok {
			if current := r.entries[id]; current != nil && current.hasTuple &&
				(current.context.State == StateActive || (current.context.State == StateClosed && now.Before(current.context.ResolveUntil))) {
				result := current.context
				r.mu.Unlock()
				return result, true
			}
		}
		if wait <= 0 {
			r.mu.Unlock()
			return Context{}, false
		}
		updates := r.updates
		r.mu.Unlock()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Context{}, false
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return Context{}, false
		case <-updates:
			stopTimer(timer)
		case <-timer.C:
			return Context{}, false
		}
	}
}

// Lookup returns a Flow by its opaque ID while it is active or still within
// the close grace period. Tag operations use this together with backend
// identity checks; callers cannot use an expired or tuple-replaced Flow.
func (r *Registry) Lookup(id flow.ID) (Context, bool) {
	if r == nil || id == "" {
		return Context{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Context{}, false
	}
	r.cleanupExpiredLocked(time.Now().UTC())
	current := r.entries[id]
	if current == nil {
		return Context{}, false
	}
	now := time.Now().UTC()
	if current.context.State == StateClosed && !now.Before(current.context.ResolveUntil) {
		return Context{}, false
	}
	return current.context, true
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

// Shutdown stops the cleanup worker and discards all in-memory mappings. It is
// separate from Close(flow.Summary), which is the flow.Observer callback.
func (r *Registry) Shutdown() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		close(r.stop)
		<-r.stopped
		r.mu.Lock()
		r.closed = true
		r.entries = make(map[flow.ID]*entry)
		r.tuples = make(map[string]flow.ID)
		r.signalLocked()
		r.mu.Unlock()
	})
	return nil
}

func (r *Registry) Stats() Stats {
	if r == nil {
		return Stats{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := Stats{Capacity: r.options.MaxEntries, BoundTuples: len(r.tuples), CapacityRejects: r.rejects.Load()}
	for _, current := range r.entries {
		if current.context.State == StateClosed {
			result.Closed++
		} else {
			result.Active++
		}
	}
	return result
}

func (r *Registry) cleanupLoop() {
	ticker := time.NewTicker(r.options.CleanupInterval)
	defer ticker.Stop()
	defer close(r.stopped)
	for {
		select {
		case <-ticker.C:
			r.mu.Lock()
			r.cleanupExpiredLocked(time.Now().UTC())
			r.mu.Unlock()
		case <-r.stop:
			return
		}
	}
}

func (r *Registry) cleanupExpiredLocked(now time.Time) {
	removed := false
	for id, current := range r.entries {
		if current.context.State != StateClosed || now.Before(current.context.ResolveUntil) {
			continue
		}
		if current.hasTuple {
			if key, err := tupleKey(current.context.BackendTuple); err == nil && r.tuples[key] == id {
				delete(r.tuples, key)
			}
		}
		delete(r.entries, id)
		removed = true
	}
	for key, id := range r.tuples {
		current := r.entries[id]
		if current == nil || !current.hasTuple {
			delete(r.tuples, key)
			removed = true
			continue
		}
		currentKey, err := tupleKey(current.context.BackendTuple)
		if err != nil || currentKey != key {
			delete(r.tuples, key)
			removed = true
		}
	}
	if removed {
		r.signalLocked()
	}
}

func (r *Registry) signalLocked() {
	close(r.updates)
	r.updates = make(chan struct{})
}

func tupleKey(tuple flow.BackendTuple) (string, error) {
	protocol := strings.ToLower(strings.TrimSpace(tuple.Protocol))
	backend := strings.TrimSpace(tuple.BackendKey)
	if (protocol != flow.ProtocolTCP && protocol != flow.ProtocolUDP) || backend == "" || !tuple.LocalAddr.IsValid() || !tuple.RemoteAddr.IsValid() {
		return "", ErrInvalidTuple
	}
	return fmt.Sprintf("%s|%s|%s|%s", protocol, backend, tuple.LocalAddr.String(), tuple.RemoteAddr.String()), nil
}

func CanonicalBackendKey(upstream string, remoteAddr string) string {
	return strings.TrimSpace(upstream) + "@" + strings.TrimSpace(remoteAddr)
}
