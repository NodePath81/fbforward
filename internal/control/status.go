package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
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
	LastActivity int64 `json:"last_activity"`
	Age          int64 `json:"age"`
}

type statusEntry struct {
	kind          string
	id            string
	clientAddr    string
	listener      string
	route         string
	startedAt     time.Time
	port          int
	upstream      string
	bytesUp       uint64
	bytesDown     uint64
	segmentsUp    uint64
	segmentsDown  uint64
	lastActivity  time.Time
	created       time.Time
	lastBroadcast time.Time
}

type StatusStore struct {
	mu  sync.Mutex
	tcp map[string]*statusEntry
	udp map[string]*statusEntry
	hub *StatusHub
}

func NewStatusStore(hub *StatusHub) *StatusStore {
	return &StatusStore{
		tcp: make(map[string]*statusEntry),
		udp: make(map[string]*statusEntry),
		hub: hub,
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
	snapshot := s.toStatusEntry(entry)
	if s.hub != nil {
		s.hub.Broadcast(statusMessage{SchemaVersion: 1, Type: "add", Entry: &snapshot})
	}
}

func (s *StatusStore) Update(id flow.ID, counters flow.Counters) {
	var entry *statusEntry
	s.mu.Lock()
	idString := id.String()
	entry = s.tcp[idString]
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
	now := time.Now()
	shouldBroadcast := entry != nil && now.Sub(entry.lastBroadcast) >= time.Second
	var snapshot *StatusEntry
	if shouldBroadcast {
		entry.lastBroadcast = now
		temp := s.toStatusEntry(entry)
		snapshot = &temp
	}
	s.mu.Unlock()
	if snapshot != nil && s.hub != nil {
		s.hub.Broadcast(statusMessage{SchemaVersion: 1, Type: "update", Entry: snapshot})
	}
}

func (s *StatusStore) Close(summary flow.Summary) {
	kind := summary.Protocol
	id := summary.ID.String()
	s.mu.Lock()
	var entry *statusEntry
	if kind == flow.ProtocolTCP {
		entry = s.tcp[id]
		delete(s.tcp, id)
	} else {
		entry = s.udp[id]
		delete(s.udp, id)
	}
	s.mu.Unlock()
	if entry != nil && s.hub != nil {
		s.hub.Broadcast(statusMessage{
			SchemaVersion: 1,
			Type:          "remove",
			Timestamp:     time.Now().UnixMilli(),
			ID:            id,
			Kind:          kind,
		})
	}
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

type TestHistoryPayload struct {
	Upstream    string  `json:"upstream"`
	Protocol    string  `json:"protocol"`
	Timestamp   int64   `json:"timestamp"`
	DurationMs  int64   `json:"duration_ms"`
	Success     bool    `json:"success"`
	RTTMs       float64 `json:"rtt_ms"`
	JitterMs    float64 `json:"jitter_ms"`
	LossRate    float64 `json:"loss_rate"`
	RetransRate float64 `json:"retrans_rate"`
	Error       string  `json:"error,omitempty"`
}

type statusErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type statusMessage struct {
	SchemaVersion       int           `json:"schema_version"`
	Type                string        `json:"type"`
	Timestamp           int64         `json:"timestamp,omitempty"`
	TCP                 []StatusEntry `json:"tcp,omitempty"`
	UDP                 []StatusEntry `json:"udp,omitempty"`
	Entry               *StatusEntry  `json:"entry,omitempty"`
	ID                  string        `json:"id,omitempty"`
	Kind                string        `json:"kind,omitempty"`
	*TestHistoryPayload `json:",omitempty"`
	*statusErrorPayload `json:",omitempty"`
}

type StatusHub struct {
	mu         sync.Mutex
	clients    map[*statusClient]struct{}
	broadcast  chan statusMessage
	ctxDone    <-chan struct{}
	logger     util.Logger
	maxClients int
}

type statusClient struct {
	send         chan []byte
	connID       string
	sendMu       sync.RWMutex
	closeOnce    sync.Once
	subscribed   bool
	intervalMs   int
	tickerCancel context.CancelFunc
}

func NewStatusHub(ctxDone <-chan struct{}, logger util.Logger) *StatusHub {
	h := &StatusHub{
		clients:    make(map[*statusClient]struct{}),
		broadcast:  make(chan statusMessage, 128),
		ctxDone:    ctxDone,
		logger:     logger,
		maxClients: 100,
	}
	go h.run()
	return h
}

func (h *StatusHub) run() {
	for {
		select {
		case <-h.ctxDone:
			h.mu.Lock()
			for client := range h.clients {
				client.close()
			}
			h.clients = make(map[*statusClient]struct{})
			h.mu.Unlock()
			return
		case msg := <-h.broadcast:
			h.mu.Lock()
			data, _ := json.Marshal(msg)
			for client := range h.clients {
				if !client.enqueue(data) {
					util.Event(h.logger, slog.LevelDebug, "control.ws.client_queue_drop",
						"ws.conn_id", client.connID,
						"queue.capacity", cap(client.send),
						"result", "dropped",
					)
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *StatusHub) CanRegister() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients) < h.maxClients
}

func (h *StatusHub) TryRegister(client *statusClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.maxClients {
		return false
	}
	h.clients[client] = struct{}{}
	return true
}

func (h *StatusHub) Unregister(client *statusClient) {
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
	client.close()
}

func (h *StatusHub) Broadcast(msg statusMessage) {
	select {
	case h.broadcast <- msg:
	default:
		util.Event(h.logger, slog.LevelDebug, "control.ws.hub_broadcast_drop",
			"queue.capacity", cap(h.broadcast),
			"result", "dropped",
		)
	}
}

func (s *StatusStore) BroadcastTestHistoryEvent(payload TestHistoryPayload) {
	s.hub.Broadcast(statusMessage{
		SchemaVersion:      1,
		Type:               "test_history_event",
		TestHistoryPayload: &payload,
	})
}

func (c *statusClient) close() {
	c.closeOnce.Do(func() {
		c.sendMu.Lock()
		defer c.sendMu.Unlock()
		close(c.send)
	})
}

func (c *statusClient) enqueue(data []byte) bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}
