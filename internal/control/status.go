package control

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/metrics"
)

type StatusEntry struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	ClientAddr string `json:"client_addr"`
	Port       int    `json:"port"`
	Upstream   string `json:"upstream"`
	BytesUp    uint64 `json:"bytes_up"`
	BytesDown  uint64 `json:"bytes_down"`
	// LastActivity is Unix milliseconds; Age is seconds since creation.
	LastActivity int64 `json:"last_activity"`
	Age          int64 `json:"age"`
}

type statusEntry struct {
	kind          string
	id            string
	clientAddr    string
	port          int
	upstream      string
	bytesUp       uint64
	bytesDown     uint64
	lastActivity  time.Time
	created       time.Time
	lastBroadcast time.Time
}

type StatusStore struct {
	mu        sync.Mutex
	tcp       map[string]*statusEntry
	udp       map[string]*statusEntry
	tcpCloser map[string]func()
	udpCloser map[string]func()
	nextID    uint64
	hub       *StatusHub
	metrics   *metrics.Metrics
}

func NewStatusStore(hub *StatusHub, metrics *metrics.Metrics) *StatusStore {
	return &StatusStore{
		tcp:       make(map[string]*statusEntry),
		udp:       make(map[string]*statusEntry),
		tcpCloser: make(map[string]func()),
		udpCloser: make(map[string]func()),
		hub:       hub,
		metrics:   metrics,
	}
}

func (s *StatusStore) AddTCP(clientAddr, upstream string, port int, closeFunc func()) string {
	return s.add("tcp", clientAddr, upstream, port, closeFunc)
}

func (s *StatusStore) AddUDP(clientAddr, upstream string, port int, closeFunc func()) string {
	return s.add("udp", clientAddr, upstream, port, closeFunc)
}

func (s *StatusStore) add(kind, clientAddr, upstream string, port int, closeFunc func()) string {
	s.mu.Lock()
	now := time.Now()
	s.nextID++
	id := kind + "-" + strconv.FormatInt(now.UnixNano(), 10) + "-" + strconv.FormatUint(s.nextID, 10)
	entry := &statusEntry{
		kind:         kind,
		id:           id,
		clientAddr:   clientAddr,
		port:         port,
		upstream:     upstream,
		lastActivity: now,
		created:      now,
	}
	if kind == "tcp" {
		s.tcp[id] = entry
		s.tcpCloser[id] = closeFunc
		if s.metrics != nil {
			s.metrics.IncTCPActive()
		}
	} else {
		s.udp[id] = entry
		s.udpCloser[id] = closeFunc
		if s.metrics != nil {
			s.metrics.IncUDPActive()
		}
	}
	s.mu.Unlock()
	snapshot := s.toStatusEntry(entry)
	s.hub.Broadcast(statusMessage{Type: "add", Entry: &snapshot})
	return id
}

func (s *StatusStore) UpdateTCP(id string, upDelta, downDelta uint64) {
	s.update("tcp", id, upDelta, downDelta)
}

func (s *StatusStore) UpdateUDP(id string, upDelta, downDelta uint64) {
	s.update("udp", id, upDelta, downDelta)
}

func (s *StatusStore) update(kind, id string, upDelta, downDelta uint64) {
	now := time.Now()
	var entry *statusEntry
	s.mu.Lock()
	if kind == "tcp" {
		entry = s.tcp[id]
	} else {
		entry = s.udp[id]
	}
	if entry != nil {
		entry.bytesUp += upDelta
		entry.bytesDown += downDelta
		entry.lastActivity = now
	}
	shouldBroadcast := entry != nil && now.Sub(entry.lastBroadcast) >= time.Second
	var snapshot *StatusEntry
	if shouldBroadcast {
		entry.lastBroadcast = now
		temp := s.toStatusEntry(entry)
		snapshot = &temp
	}
	s.mu.Unlock()
	if snapshot != nil {
		s.hub.Broadcast(statusMessage{Type: "update", Entry: snapshot})
	}
}

func (s *StatusStore) RemoveTCP(id string) {
	s.remove("tcp", id)
}

func (s *StatusStore) RemoveUDP(id string) {
	s.remove("udp", id)
}

func (s *StatusStore) remove(kind, id string) {
	s.mu.Lock()
	var entry *statusEntry
	if kind == "tcp" {
		entry = s.tcp[id]
		delete(s.tcp, id)
		delete(s.tcpCloser, id)
		if s.metrics != nil {
			s.metrics.DecTCPActive()
		}
	} else {
		entry = s.udp[id]
		delete(s.udp, id)
		delete(s.udpCloser, id)
		if s.metrics != nil {
			s.metrics.DecUDPActive()
		}
	}
	s.mu.Unlock()
	if entry != nil {
		s.hub.Broadcast(statusMessage{Type: "remove", ID: id, Kind: kind})
	}
}

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

func (s *StatusStore) CloseByUpstream(tag string) {
	var closers []func()
	s.mu.Lock()
	for id, entry := range s.tcp {
		if entry.upstream == tag {
			closers = append(closers, s.tcpCloser[id])
		}
	}
	for id, entry := range s.udp {
		if entry.upstream == tag {
			closers = append(closers, s.udpCloser[id])
		}
	}
	s.mu.Unlock()
	for _, closer := range closers {
		if closer != nil {
			closer()
		}
	}
}

func (s *StatusStore) CloseAll() {
	var closers []func()
	s.mu.Lock()
	for _, closer := range s.tcpCloser {
		closers = append(closers, closer)
	}
	for _, closer := range s.udpCloser {
		closers = append(closers, closer)
	}
	s.mu.Unlock()
	for _, closer := range closers {
		if closer != nil {
			closer()
		}
	}
}

func (s *StatusStore) toStatusEntry(entry *statusEntry) StatusEntry {
	age := time.Since(entry.created).Seconds()
	return StatusEntry{
		Kind:         entry.kind,
		ID:           entry.id,
		ClientAddr:   entry.clientAddr,
		Port:         entry.port,
		Upstream:     entry.upstream,
		BytesUp:      entry.bytesUp,
		BytesDown:    entry.bytesDown,
		LastActivity: entry.lastActivity.UnixMilli(),
		Age:          int64(age),
	}
}

type statusMessage struct {
	Type  string        `json:"type"`
	TCP   []StatusEntry `json:"tcp,omitempty"`
	UDP   []StatusEntry `json:"udp,omitempty"`
	Entry *StatusEntry  `json:"entry,omitempty"`
	ID    string        `json:"id,omitempty"`
	Kind  string        `json:"kind,omitempty"`
}

type StatusHub struct {
	mu        sync.Mutex
	clients   map[*statusClient]struct{}
	broadcast chan statusMessage
	ctxDone   <-chan struct{}
}

type statusClient struct {
	send      chan []byte
	closeOnce sync.Once
}

func NewStatusHub(ctxDone <-chan struct{}) *StatusHub {
	h := &StatusHub{
		clients:   make(map[*statusClient]struct{}),
		broadcast: make(chan statusMessage, 128),
		ctxDone:   ctxDone,
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
				select {
				case client.send <- data:
				default:
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *StatusHub) Register(client *statusClient) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
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
	}
}

func (c *statusClient) close() {
	c.closeOnce.Do(func() {
		close(c.send)
	})
}
