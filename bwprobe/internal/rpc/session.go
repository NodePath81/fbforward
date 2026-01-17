package rpc

import (
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/metrics"
	"github.com/NodePath81/fbforward/bwprobe/internal/network"
	"github.com/NodePath81/fbforward/bwprobe/internal/transport"

	"github.com/google/uuid"
)

const (
	defaultIntervalDuration = 100 * time.Millisecond
	sessionCleanupInterval  = 30 * time.Second
	sessionTimeout          = 60 * time.Second
	udpPingMaxAge           = 15 * time.Minute
)

const (
	DefaultSessionCleanupInterval = sessionCleanupInterval
	DefaultSessionTimeout         = sessionTimeout
	DefaultUDPPingMaxAge          = udpPingMaxAge
)

// intervalBucket tracks bytes and out-of-order packets per interval
type intervalBucket struct {
	bytes uint64
	ooo   uint64
}

// SessionState manages state for a single client session
type SessionState struct {
	mu            sync.Mutex
	sessionID     string
	clientAddr    net.Addr
	created       time.Time
	lastHeartbeat time.Time
	capabilities  ClientCapabilities

	// Sample state
	intervalDur time.Duration
	active      bool
	sampleID    uint32
	startTime   time.Time
	firstByte   time.Time
	lastByte    time.Time
	totalBytes  uint64
	intervals   []intervalBucket
	baseSeq     uint64
	maxSeq      uint64
	hasSeq      bool
	packetsRecv uint64

	// Connection references
	dataConn      net.Conn     // TCP data connection
	reverseConn   *net.TCPConn // TCP reverse connection
	udpAddr       *net.UDPAddr // Registered UDP endpoint
	udpRegistered bool

	// Reverse mode
	reverseActive bool
	reverseStopCh chan struct{}
	reverseDoneCh chan struct{}
	reverseConnReady chan struct{}

	tcpSendBufferBytes uint64
	tcpRetransmits     uint64
	tcpSegmentsSent    uint64
}

// SessionManager manages all active sessions
type SessionManager struct {
	mu        sync.RWMutex
	sessions  map[string]*SessionState // indexed by session_id
	interval  time.Duration
	recvWait  time.Duration
	udpPingMu sync.Mutex
	udpPing   map[string]time.Time
}

// NewSessionManager creates a new session manager
func NewSessionManager(recvWait time.Duration) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*SessionState),
		interval: defaultIntervalDuration,
		recvWait: recvWait,
		udpPing:  make(map[string]time.Time),
	}
}

// CreateSession creates a new session with unique ID
func (sm *SessionManager) CreateSession(clientAddr net.Addr, caps ClientCapabilities) *SessionState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionID := uuid.New().String()
	now := time.Now()
	session := &SessionState{
		sessionID:     sessionID,
		clientAddr:    clientAddr,
		created:       now,
		lastHeartbeat: now,
		capabilities:  caps,
		intervalDur:   sm.interval,
	}

	sm.sessions[sessionID] = session
	return session
}

// GetSession retrieves session by ID
func (sm *SessionManager) GetSession(sessionID string) (*SessionState, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[sessionID]
	return session, ok
}

// UpdateHeartbeat updates last heartbeat time
func (sm *SessionManager) UpdateHeartbeat(sessionID string) error {
	sm.mu.RLock()
	session, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		return NewRPCError(ErrInvalidSession, "Session not found", nil)
	}

	session.mu.Lock()
	session.lastHeartbeat = time.Now()
	session.mu.Unlock()
	return nil
}

// CleanupExpiredSessions removes sessions with no recent heartbeat
func (sm *SessionManager) CleanupExpiredSessions(timeout time.Duration) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	count := 0

	for sessionID, session := range sm.sessions {
		session.mu.Lock()
		expired := now.Sub(session.lastHeartbeat) > timeout
		session.mu.Unlock()

		if expired {
			// Close all connections
			if session.dataConn != nil {
				session.dataConn.Close()
			}
			if session.reverseConn != nil {
				session.reverseConn.Close()
			}
			delete(sm.sessions, sessionID)
			count++
		}
	}

	return count
}

// DeleteSession removes a session and closes its connections
func (sm *SessionManager) DeleteSession(sessionID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return NewRPCError(ErrInvalidSession, "Session not found", nil)
	}

	// Close all connections
	if session.dataConn != nil {
		session.dataConn.Close()
	}
	if session.reverseConn != nil {
		session.reverseConn.Close()
	}

	delete(sm.sessions, sessionID)
	return nil
}

// ActiveSessionCount returns the number of active sessions
func (sm *SessionManager) ActiveSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// RecordUDPPing stores the last UDP ping time for an address.
func (sm *SessionManager) RecordUDPPing(addr *net.UDPAddr) {
	if addr == nil {
		return
	}
	key := addr.String()
	sm.udpPingMu.Lock()
	sm.udpPing[key] = time.Now()
	sm.udpPingMu.Unlock()
}

// RecentUDPPing checks whether a recent UDP ping was observed from the address.
func (sm *SessionManager) RecentUDPPing(addr *net.UDPAddr, window time.Duration) bool {
	if addr == nil {
		return false
	}
	key := addr.String()
	sm.udpPingMu.Lock()
	ts, ok := sm.udpPing[key]
	sm.udpPingMu.Unlock()
	if !ok {
		return false
	}
	return time.Since(ts) <= window
}

// CleanupExpiredUDPPings removes UDP ping records older than maxAge.
func (sm *SessionManager) CleanupExpiredUDPPings(maxAge time.Duration) int {
	sm.udpPingMu.Lock()
	defer sm.udpPingMu.Unlock()

	now := time.Now()
	count := 0
	for key, ts := range sm.udpPing {
		if now.Sub(ts) > maxAge {
			delete(sm.udpPing, key)
			count++
		}
	}
	return count
}

// SessionState methods

// StartSample initializes a new sample
func (s *SessionState) StartSample(sampleID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.sampleID = sampleID
	s.startTime = time.Time{}
	s.firstByte = time.Time{}
	s.lastByte = time.Time{}
	s.totalBytes = 0
	s.intervals = s.intervals[:0]
	s.baseSeq = 0
	s.maxSeq = 0
	s.hasSeq = false
	s.packetsRecv = 0
	s.tcpSendBufferBytes = 0
	s.tcpRetransmits = 0
	s.tcpSegmentsSent = 0
	s.lastHeartbeat = time.Now()
}

// StopSample finalizes a sample and returns the report
func (s *SessionState) StopSample(sampleID uint32) (SampleStopResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active {
		return SampleStopResponse{}, NewRPCError(ErrSampleNotFound, "No active sample", nil)
	}
	if s.sampleID != sampleID {
		return SampleStopResponse{}, NewRPCError(ErrSampleIDMismatch, "Sample ID mismatch", nil)
	}

	s.active = false
	s.sampleID = 0

	report := SampleStopResponse{
		SampleID: sampleID,
	}

	if s.firstByte.IsZero() || s.lastByte.IsZero() {
		return report, nil
	}

	report.TotalBytes = s.totalBytes
	report.FirstByteTime = s.firstByte.Format(time.RFC3339Nano)
	report.LastByteTime = s.lastByte.Format(time.RFC3339Nano)
	report.TotalDuration = s.lastByte.Sub(s.firstByte).Seconds()
	if report.TotalDuration > 0 {
		report.AvgThroughputBps = float64(s.totalBytes*8) / report.TotalDuration
	}

	intervalCount := len(s.intervals)
	report.Intervals = make([]IntervalReport, 0, intervalCount)
	for i, interval := range s.intervals {
		duration := s.intervalDur
		if i == intervalCount-1 {
			start := s.startTime.Add(time.Duration(i) * s.intervalDur)
			delta := s.lastByte.Sub(start)
			if delta < 0 {
				delta = 0
			}
			duration = delta
		}
		report.Intervals = append(report.Intervals, IntervalReport{
			Bytes:      interval.bytes,
			DurationMs: int64(duration / time.Millisecond),
			OOOCount:   interval.ooo,
		})
	}

	if s.hasSeq {
		total := s.maxSeq - s.baseSeq + 1
		lost := uint64(0)
		if total > s.packetsRecv {
			lost = total - s.packetsRecv
		}
		report.PacketsRecv = s.packetsRecv
		report.PacketsLost = lost
	}
	if s.tcpSendBufferBytes > 0 {
		report.TCPSendBufferBytes = s.tcpSendBufferBytes
	}
	if s.tcpRetransmits > 0 || s.tcpSegmentsSent > 0 {
		report.TCPRetransmits = s.tcpRetransmits
		report.TCPSegmentsSent = s.tcpSegmentsSent
	}

	return report, nil
}

// RecordSample records bytes received in a sample
func (s *SessionState) RecordSample(now time.Time, sampleID uint32, bytes int, ooo bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active || s.sampleID != sampleID {
		return
	}
	s.lastHeartbeat = now

	if s.firstByte.IsZero() {
		s.firstByte = now
		s.startTime = now
	}
	s.lastByte = now

	idx := int(now.Sub(s.startTime) / s.intervalDur)
	for len(s.intervals) <= idx {
		s.intervals = append(s.intervals, intervalBucket{})
	}

	s.intervals[idx].bytes += uint64(bytes)
	if ooo {
		s.intervals[idx].ooo++
	}
	s.totalBytes += uint64(bytes)
}

// RecordUDPPacket records a UDP packet with sequence tracking
func (s *SessionState) RecordUDPPacket(now time.Time, sampleID uint32, seq uint64, bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active || s.sampleID != sampleID {
		return
	}
	s.lastHeartbeat = now

	if s.firstByte.IsZero() {
		s.firstByte = now
		s.startTime = now
	}
	s.lastByte = now

	ooo := false
	if !s.hasSeq {
		s.baseSeq = seq
		s.maxSeq = seq
		s.hasSeq = true
	} else {
		if seq < s.maxSeq {
			ooo = true
		} else {
			s.maxSeq = seq
		}
	}
	s.packetsRecv++

	idx := int(now.Sub(s.startTime) / s.intervalDur)
	for len(s.intervals) <= idx {
		s.intervals = append(s.intervals, intervalBucket{})
	}

	s.intervals[idx].bytes += uint64(bytes)
	if ooo {
		s.intervals[idx].ooo++
	}
	s.totalBytes += uint64(bytes)
}

// RegisterDataConnection associates a data connection with session
func (s *SessionState) RegisterDataConnection(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dataConn = conn
	s.lastHeartbeat = time.Now()
}

// RegisterReverseConnection associates a reverse connection with session
func (s *SessionState) RegisterReverseConnection(conn *net.TCPConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reverseConn = conn
	if s.reverseConnReady != nil {
		close(s.reverseConnReady)
		s.reverseConnReady = nil
	}
	s.lastHeartbeat = time.Now()
}

// ClearReverseConnection clears a reverse connection if it matches the active one.
func (s *SessionState) ClearReverseConnection(conn *net.TCPConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reverseConn == conn {
		s.reverseConn = nil
	}
}

// RegisterUDPEndpoint stores validated UDP endpoint
func (s *SessionState) RegisterUDPEndpoint(addr *net.UDPAddr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.udpAddr = addr
	s.udpRegistered = true
	s.lastHeartbeat = time.Now()
}

// GetUDPEndpoint retrieves registered UDP endpoint
func (s *SessionState) GetUDPEndpoint() (*net.UDPAddr, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.udpAddr, s.udpRegistered
}

// IsActive checks if a sample is currently active
func (s *SessionState) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// IsReverseActive reports whether a reverse sample is active.
func (s *SessionState) IsReverseActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reverseActive
}

// ShouldWait checks if the session is active and has received data
func (s *SessionState) ShouldWait() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active && !s.firstByte.IsZero()
}

type ReverseConfig struct {
	BandwidthBps float64
	ChunkSize    int64
	RTT          time.Duration
	SampleBytes  int64
}

func (s *SessionState) StartReverseTCP(sampleID uint32, cfg ReverseConfig) *Error {
	payloadSize, err := network.TCPPayloadSize(cfg.ChunkSize)
	if err != nil {
		return NewRPCError(ErrInvalidParams, err.Error(), nil)
	}
	if cfg.SampleBytes <= 0 {
		return NewRPCError(ErrInvalidSampleSize, "sample bytes must be > 0", nil)
	}
	conn := s.waitReverseTCP(2 * time.Second)
	if conn == nil {
		return NewRPCError(ErrReverseNotAvailable, "reverse tcp connection not available", nil)
	}

	s.mu.Lock()
	if s.reverseActive {
		s.mu.Unlock()
		return NewRPCError(ErrSampleAlreadyActive, "sample already active", nil)
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	s.reverseActive = true
	s.reverseStopCh = stopCh
	s.reverseDoneCh = doneCh
	s.mu.Unlock()

	s.StartSample(sampleID)
	record := func(now time.Time, bytes int) {
		s.RecordSample(now, sampleID, bytes, false)
	}
	writeBuf, err := transport.StartTCPReverseSender(conn, sampleID, payloadSize, cfg.SampleBytes, cfg.BandwidthBps, cfg.RTT, record, stopCh, doneCh)
	if err != nil {
		return NewRPCError(ErrServerError, err.Error(), nil)
	}
	s.mu.Lock()
	s.tcpSendBufferBytes = uint64(writeBuf)
	s.mu.Unlock()

	return nil
}

func (s *SessionState) StartReverseUDP(sampleID uint32, conn *net.UDPConn, cfg ReverseConfig) *Error {
	if conn == nil {
		return NewRPCError(ErrServerError, "udp connection unavailable", nil)
	}
	payloadSize, _, err := network.UDPPayloadSize(cfg.ChunkSize)
	if err != nil {
		return NewRPCError(ErrInvalidParams, err.Error(), nil)
	}
	if cfg.SampleBytes <= 0 {
		return NewRPCError(ErrInvalidSampleSize, "sample bytes must be > 0", nil)
	}

	s.mu.Lock()
	if s.reverseActive {
		s.mu.Unlock()
		return NewRPCError(ErrSampleAlreadyActive, "sample already active", nil)
	}
	udpAddr := s.udpAddr
	if !s.udpRegistered || udpAddr == nil {
		s.mu.Unlock()
		return NewRPCError(ErrUDPNotRegistered, "udp endpoint not registered", nil)
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	s.reverseActive = true
	s.reverseStopCh = stopCh
	s.reverseDoneCh = doneCh
	s.mu.Unlock()

	s.StartSample(sampleID)
	record := func(now time.Time, bytes int) {
		s.RecordSample(now, sampleID, bytes, false)
	}
	if err := transport.StartUDPReverseSender(conn, udpAddr, sampleID, payloadSize, cfg.SampleBytes, cfg.BandwidthBps, record, stopCh, doneCh); err != nil {
		return NewRPCError(ErrServerError, err.Error(), nil)
	}

	return nil
}

func (s *SessionState) StopReverse(sampleID uint32) (SampleStopResponse, error) {
	s.mu.Lock()
	if !s.reverseActive {
		s.mu.Unlock()
		return SampleStopResponse{}, NewRPCError(ErrSampleNotFound, "no active reverse sample", nil)
	}
	if s.sampleID != sampleID {
		s.mu.Unlock()
		return SampleStopResponse{}, NewRPCError(ErrSampleIDMismatch, "sample id mismatch", nil)
	}
	stopCh := s.reverseStopCh
	doneCh := s.reverseDoneCh
	conn := s.reverseConn
	s.mu.Unlock()

	select {
	case <-stopCh:
	default:
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
	s.mu.Lock()
	s.reverseActive = false
	s.reverseStopCh = nil
	s.reverseDoneCh = nil
	s.mu.Unlock()
	if conn != nil {
		if info, err := metrics.ReadTCPStats(conn); err == nil {
			s.mu.Lock()
			s.tcpRetransmits = info.Retransmits
			s.tcpSegmentsSent = info.SegmentsSent
			s.mu.Unlock()
		}
	}
	return s.StopSample(sampleID)
}

func (s *SessionState) waitReverseTCP(timeout time.Duration) *net.TCPConn {
	s.mu.Lock()
	if s.reverseConn != nil {
		conn := s.reverseConn
		s.mu.Unlock()
		return conn
	}
	ready := make(chan struct{})
	s.reverseConnReady = ready
	s.mu.Unlock()

	select {
	case <-ready:
		s.mu.Lock()
		conn := s.reverseConn
		s.mu.Unlock()
		return conn
	case <-time.After(timeout):
		return nil
	}
}
