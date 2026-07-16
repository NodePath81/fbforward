package control

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

type StatusEntry struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	ClientAddr   string `json:"client_addr"`
	Listener     string `json:"listener,omitempty"`
	Route        string `json:"route,omitempty"`
	StartedAt    int64  `json:"started_at,omitempty"`
	Port         int    `json:"port"`
	Upstream     string `json:"upstream"`
	BytesUp      uint64 `json:"bytes_up"`
	BytesDown    uint64 `json:"bytes_down"`
	SegmentsUp   uint64 `json:"segments_up"`
	SegmentsDown uint64 `json:"segments_down"`
	// LastActivity is Unix milliseconds; Age is seconds since creation.
	LastActivity int64         `json:"last_activity"`
	Age          int64         `json:"age"`
	Tags         []FlowTagView `json:"tags,omitempty"`
}

type FlowTagView struct {
	Tag   string `json:"tag"`
	Scope string `json:"scope"`
}

type statusEntry struct {
	kind         string
	id           string
	clientAddr   string
	listener     string
	route        string
	startedAt    time.Time
	port         int
	upstream     string
	bytesUp      uint64
	bytesDown    uint64
	segmentsUp   uint64
	segmentsDown uint64
	lastActivity time.Time
	created      time.Time
}

type StatusStore struct {
	mu  sync.Mutex
	tcp map[string]*statusEntry
	udp map[string]*statusEntry
}

func NewStatusStore() *StatusStore {
	return &StatusStore{
		tcp: make(map[string]*statusEntry),
		udp: make(map[string]*statusEntry),
	}
}

func (s *StatusStore) Open(meta flow.Meta) {
	s.mu.Lock()
	id := meta.ID.String()
	now := meta.StartedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	entry := &statusEntry{
		kind:         meta.Protocol,
		id:           id,
		clientAddr:   meta.ClientAddr.String(),
		listener:     meta.Listener,
		route:        meta.Route,
		startedAt:    now,
		port:         listenerPort(meta.Listener),
		upstream:     meta.Upstream,
		lastActivity: now,
		created:      now,
	}
	if meta.Protocol == flow.ProtocolTCP {
		s.tcp[id] = entry
	} else {
		s.udp[id] = entry
	}
	s.mu.Unlock()
}

func (s *StatusStore) Update(id flow.ID, counters flow.Counters) {
	s.mu.Lock()
	idString := id.String()
	entry := s.tcp[idString]
	if entry == nil {
		entry = s.udp[idString]
	}
	if entry != nil {
		entry.bytesUp = counters.BytesUp
		entry.bytesDown = counters.BytesDown
		entry.segmentsUp = counters.SegmentsUp
		entry.segmentsDown = counters.SegmentsDown
		if !counters.LastActivity.IsZero() {
			entry.lastActivity = counters.LastActivity
		}
	}
	s.mu.Unlock()
}

func (s *StatusStore) Close(summary flow.Summary) {
	kind := summary.Protocol
	id := summary.ID.String()
	s.mu.Lock()
	if kind == flow.ProtocolTCP {
		delete(s.tcp, id)
	} else {
		delete(s.udp, id)
	}
	s.mu.Unlock()
}

func (s *StatusStore) Reject(flow.Rejection) {}

func (s *StatusStore) Snapshot() (tcp []StatusEntry, udp []StatusEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tcp = make([]StatusEntry, 0, len(s.tcp))
	for _, entry := range s.tcp {
		tcp = append(tcp, s.toStatusEntry(entry))
	}
	udp = make([]StatusEntry, 0, len(s.udp))
	for _, entry := range s.udp {
		udp = append(udp, s.toStatusEntry(entry))
	}
	return tcp, udp
}

func (s *StatusStore) toStatusEntry(entry *statusEntry) StatusEntry {
	age := time.Since(entry.created).Seconds()
	return StatusEntry{
		Kind:         entry.kind,
		ID:           entry.id,
		ClientAddr:   entry.clientAddr,
		Listener:     entry.listener,
		Route:        entry.route,
		StartedAt:    entry.startedAt.UnixMilli(),
		Port:         entry.port,
		Upstream:     entry.upstream,
		BytesUp:      entry.bytesUp,
		BytesDown:    entry.bytesDown,
		SegmentsUp:   entry.segmentsUp,
		SegmentsDown: entry.segmentsDown,
		LastActivity: entry.lastActivity.UnixMilli(),
		Age:          int64(age),
	}
}

func listenerPort(listener string) int {
	if _, port, err := net.SplitHostPort(listener); err == nil {
		var value int
		if _, err := fmt.Sscanf(port, "%d", &value); err == nil {
			return value
		}
	}
	return 0
}
