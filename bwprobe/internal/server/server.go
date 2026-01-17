package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/metrics"
	"github.com/NodePath81/fbforward/bwprobe/internal/network"
	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
	"github.com/NodePath81/fbforward/bwprobe/internal/rpc"
	"github.com/NodePath81/fbforward/bwprobe/internal/transport"
)

const (
	intervalDuration = 100 * time.Millisecond
	maxTCPFrameBytes = 4 * 1024 * 1024
	maxRPCMessageSize = 10 * 1024 * 1024
	tcpReadBufferSize = 256 * 1024
)

type intervalReport struct {
	Bytes      uint64 `json:"bytes"`
	DurationMs int64  `json:"duration_ms"`
	OOOCount   uint64 `json:"ooo_count"`
}

type sampleReport struct {
	SampleID      uint32           `json:"sample_id"`
	TotalBytes    uint64           `json:"total_bytes"`
	TotalDuration float64          `json:"total_duration"`
	Intervals     []intervalReport `json:"intervals"`
	FirstByteTime string           `json:"first_byte_time"`
	LastByteTime  string           `json:"last_byte_time"`
	AvgThroughput float64          `json:"avg_throughput,omitempty"`
	PacketsRecv   uint64           `json:"packets_recv,omitempty"`
	PacketsLost   uint64           `json:"packets_lost,omitempty"`
	TCPSendBuffer uint64           `json:"tcp_send_buffer_bytes,omitempty"`
	TCPRetrans    uint64           `json:"tcp_retransmits,omitempty"`
	TCPSegs       uint64           `json:"tcp_segments_sent,omitempty"`
}

type intervalBucket struct {
	bytes uint64
	ooo   uint64
}

type reverseConfig struct {
	bandwidthBps float64
	chunkSize    int64
	rtt          time.Duration
	sampleBytes  int64
	udpPort      int
}

type clientState struct {
	mu          sync.Mutex
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

	reverseActive bool
	reverseStopCh chan struct{}
	reverseDoneCh chan struct{}
	reverseTCP    *net.TCPConn
	reverseConnReady chan struct{}

	tcpSendBufferBytes uint64
	tcpRetransmits     uint64
	tcpSegmentsSent    uint64
}

type serverState struct {
	mu         sync.Mutex
	clients    map[string]*clientState
	interval   time.Duration
	recvWait   time.Duration
	udpConn    *net.UDPConn
	sessionMgr *rpc.SessionManager // RPC session manager
}

type Config struct {
	Port     int
	RecvWait time.Duration
}

func newServerState(recvWait time.Duration) *serverState {
	return &serverState{
		clients:    make(map[string]*clientState),
		interval:   intervalDuration,
		recvWait:   recvWait,
		sessionMgr: rpc.NewSessionManager(recvWait),
	}
}

func (s *serverState) client(key string) *clientState {
	s.mu.Lock()
	defer s.mu.Unlock()
	client := s.clients[key]
	if client == nil {
		client = &clientState{intervalDur: s.interval}
		s.clients[key] = client
	}
	return client
}

func (c *clientState) startSample(sampleID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = true
	c.sampleID = sampleID
	c.startTime = time.Time{}
	c.firstByte = time.Time{}
	c.lastByte = time.Time{}
	c.totalBytes = 0
	c.intervals = c.intervals[:0]
	c.baseSeq = 0
	c.maxSeq = 0
	c.hasSeq = false
	c.packetsRecv = 0
	c.tcpSendBufferBytes = 0
	c.tcpRetransmits = 0
	c.tcpSegmentsSent = 0
}

func (c *clientState) stopSample(sampleID uint32) (sampleReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return sampleReport{}, errors.New("no active sample")
	}
	if c.sampleID != sampleID {
		return sampleReport{}, fmt.Errorf("sample id mismatch (active %d)", c.sampleID)
	}
	c.active = false
	c.sampleID = 0

	report := sampleReport{}
	report.SampleID = sampleID
	if c.firstByte.IsZero() || c.lastByte.IsZero() {
		return report, nil
	}

	report.TotalBytes = c.totalBytes
	report.FirstByteTime = c.firstByte.Format(time.RFC3339Nano)
	report.LastByteTime = c.lastByte.Format(time.RFC3339Nano)
	report.TotalDuration = c.lastByte.Sub(c.firstByte).Seconds()
	if report.TotalDuration > 0 {
		report.AvgThroughput = float64(c.totalBytes*8) / report.TotalDuration
	}

	intervalCount := len(c.intervals)
	report.Intervals = make([]intervalReport, 0, intervalCount)
	for i, interval := range c.intervals {
		duration := c.intervalDur
		if i == intervalCount-1 {
			start := c.startTime.Add(time.Duration(i) * c.intervalDur)
			delta := c.lastByte.Sub(start)
			if delta < 0 {
				delta = 0
			}
			duration = delta
		}
		report.Intervals = append(report.Intervals, intervalReport{
			Bytes:      interval.bytes,
			DurationMs: int64(duration / time.Millisecond),
			OOOCount:   interval.ooo,
		})
	}

	if c.hasSeq {
		total := c.maxSeq - c.baseSeq + 1
		lost := uint64(0)
		if total > c.packetsRecv {
			lost = total - c.packetsRecv
		}
		report.PacketsRecv = c.packetsRecv
		report.PacketsLost = lost
	}
	if c.tcpSendBufferBytes > 0 {
		report.TCPSendBuffer = c.tcpSendBufferBytes
	}
	if c.tcpRetransmits > 0 || c.tcpSegmentsSent > 0 {
		report.TCPRetrans = c.tcpRetransmits
		report.TCPSegs = c.tcpSegmentsSent
	}

	return report, nil
}

func (c *clientState) shouldWait() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active && !c.firstByte.IsZero()
}

func (c *clientState) recordSample(now time.Time, sampleID uint32, bytes int, ooo bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active || c.sampleID != sampleID {
		return
	}
	if c.firstByte.IsZero() {
		c.firstByte = now
		c.startTime = now
	}
	c.lastByte = now
	idx := int(now.Sub(c.startTime) / c.intervalDur)
	if idx < 0 {
		idx = 0
	}
	for len(c.intervals) <= idx {
		c.intervals = append(c.intervals, intervalBucket{})
	}
	c.intervals[idx].bytes += uint64(bytes)
	if ooo {
		c.intervals[idx].ooo++
	}
	c.totalBytes += uint64(bytes)
}

func (c *clientState) recordUDP(now time.Time, sampleID uint32, bytes int, seq uint64) {
	ooo := false
	c.mu.Lock()
	if !c.active || c.sampleID != sampleID {
		c.mu.Unlock()
		return
	}
	if !c.hasSeq {
		c.baseSeq = seq
		c.maxSeq = seq
		c.hasSeq = true
	} else {
		if seq > c.maxSeq {
			c.maxSeq = seq
		} else if seq < c.maxSeq {
			ooo = true
		}
	}
	c.packetsRecv++
	c.mu.Unlock()

	c.recordSample(now, sampleID, bytes, ooo)
}

func (c *clientState) setReverseTCPConn(conn *net.TCPConn) {
	c.mu.Lock()
	if c.reverseTCP != nil && c.reverseTCP != conn {
		_ = c.reverseTCP.Close()
	}
	c.reverseTCP = conn
	if c.reverseConnReady != nil {
		close(c.reverseConnReady)
		c.reverseConnReady = nil
	}
	c.mu.Unlock()
}

func (c *clientState) clearReverseTCPConn(conn *net.TCPConn) {
	c.mu.Lock()
	if c.reverseTCP == conn {
		c.reverseTCP = nil
	}
	c.mu.Unlock()
}

func (c *clientState) waitReverseTCP(timeout time.Duration) *net.TCPConn {
	c.mu.Lock()
	if c.reverseTCP != nil {
		conn := c.reverseTCP
		c.mu.Unlock()
		return conn
	}
	ready := make(chan struct{})
	c.reverseConnReady = ready
	c.mu.Unlock()

	select {
	case <-ready:
		c.mu.Lock()
		conn := c.reverseTCP
		c.mu.Unlock()
		return conn
	case <-time.After(timeout):
		return nil
	}
}

func (c *clientState) isReverseActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reverseActive
}

func (c *clientState) startReverseTCP(sampleID uint32, cfg reverseConfig) error {
	payloadSize, err := network.TCPPayloadSize(cfg.chunkSize)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.reverseActive {
		c.mu.Unlock()
		return errors.New("reverse sample already active")
	}
	c.mu.Unlock()

	conn := c.waitReverseTCP(2 * time.Second)
	if conn == nil {
		return errors.New("reverse tcp connection not available")
	}

	c.mu.Lock()
	if c.reverseActive {
		c.mu.Unlock()
		return errors.New("reverse sample already active")
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	c.reverseActive = true
	c.reverseStopCh = stopCh
	c.reverseDoneCh = doneCh
	c.mu.Unlock()

	c.startSample(sampleID)
	record := func(now time.Time, bytes int) {
		c.recordSample(now, sampleID, bytes, false)
	}
	writeBuf, err := transport.StartTCPReverseSender(conn, sampleID, payloadSize, cfg.sampleBytes, cfg.bandwidthBps, cfg.rtt, record, stopCh, doneCh)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.tcpSendBufferBytes = uint64(writeBuf)
	c.mu.Unlock()

	return nil
}

func (c *clientState) startReverseUDP(sampleID uint32, conn *net.UDPConn, addr *net.UDPAddr, cfg reverseConfig) error {
	payloadSize, _, err := network.UDPPayloadSize(cfg.chunkSize)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.reverseActive {
		c.mu.Unlock()
		return errors.New("reverse sample already active")
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	c.reverseActive = true
	c.reverseStopCh = stopCh
	c.reverseDoneCh = doneCh
	c.mu.Unlock()

	c.startSample(sampleID)
	record := func(now time.Time, bytes int) {
		c.recordSample(now, sampleID, bytes, false)
	}
	if err := transport.StartUDPReverseSender(conn, addr, sampleID, payloadSize, cfg.sampleBytes, cfg.bandwidthBps, record, stopCh, doneCh); err != nil {
		return err
	}

	return nil
}

func (c *clientState) stopReverse(sampleID uint32) (sampleReport, error) {
	c.mu.Lock()
	if !c.reverseActive {
		c.mu.Unlock()
		return sampleReport{}, errors.New("no active reverse sample")
	}
	if c.sampleID != sampleID {
		c.mu.Unlock()
		return sampleReport{}, fmt.Errorf("sample id mismatch (active %d)", c.sampleID)
	}
	stopCh := c.reverseStopCh
	doneCh := c.reverseDoneCh
	c.mu.Unlock()

	select {
	case <-stopCh:
	default:
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
	c.mu.Lock()
	c.reverseActive = false
	c.reverseStopCh = nil
	c.reverseDoneCh = nil
	c.mu.Unlock()
	if conn := c.reverseTCP; conn != nil {
		if info, err := metrics.ReadTCPStats(conn); err == nil {
			c.mu.Lock()
			c.tcpRetransmits = info.Retransmits
			c.tcpSegmentsSent = info.SegmentsSent
			c.mu.Unlock()
		}
	}
	return c.stopSample(sampleID)
}

func clientKey(addr net.Addr) string {
	return addr.String()
}

// Run starts the quality metrics server.
func Run(cfg Config) {
	state := newServerState(cfg.RecvWait)

	tcpListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		fmt.Printf("error: cannot listen on TCP port %d: %v\n", cfg.Port, err)
		return
	}
	defer tcpListener.Close()

	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		fmt.Printf("error: cannot resolve UDP port %d: %v\n", cfg.Port, err)
		return
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Printf("error: cannot listen on UDP port %d: %v\n", cfg.Port, err)
		return
	}
	defer udpConn.Close()
	state.udpConn = udpConn

	fmt.Printf("Network quality server listening on port %d (tcp/udp)\n", cfg.Port)

	go runUDP(udpConn, state)

	// Start session cleanup goroutine for RPC sessions
	go func() {
		ticker := time.NewTicker(rpc.DefaultSessionCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			cleaned := state.sessionMgr.CleanupExpiredSessions(rpc.DefaultSessionTimeout)
			pingsCleaned := state.sessionMgr.CleanupExpiredUDPPings(rpc.DefaultUDPPingMaxAge)
			if cleaned > 0 || pingsCleaned > 0 {
				fmt.Printf("Cleaned up %d sessions, %d UDP pings\n", cleaned, pingsCleaned)
			}
		}
	}()

	for {
		conn, err := tcpListener.Accept()
		if err != nil {
			continue
		}
		go handleTCP(conn, state)
	}
}

func handleTCP(conn net.Conn, state *serverState) {
	defer conn.Close()
	key := clientKey(conn.RemoteAddr())

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}

	switch string(header) {
	case "RPC\x00":
		// NEW: JSON-RPC protocol with session management
		rpcServer := rpc.NewRPCServer(state.sessionMgr, state.udpConn)
		_ = rpcServer.Handle(context.Background(), conn)
		return
	case protocol.TCPPingHeader:
		_, _ = conn.Write([]byte(protocol.TCPPongHeader))
		return
	case protocol.TCPControlHeader:
		// LEGACY: Old text protocol
		handleControl(conn, state, key)
		return
	case protocol.TCPDataHeader:
		handleTCPData(conn, state, key)
		return
	case protocol.TCPReverseHeader:
		handleTCPReverse(conn, state, key)
		return
	default:
		return
	}
}

func handleTCPData(conn net.Conn, state *serverState, key string) {
	reader := bufio.NewReader(conn)

	// Try to read session ID (RPC mode) without consuming legacy bytes.
	var session *rpc.SessionState
	if sessionID, ok := peekSessionID(reader); ok {
		if sess, found := state.sessionMgr.GetSession(sessionID); found {
			session = sess
			session.RegisterDataConnection(conn)
		}
	}

	// Use RPC session if available, otherwise legacy client state
	var legacyClient *clientState
	if session == nil {
		legacyClient = state.client(key)
	}

	header := make([]byte, protocol.TCPFrameHeaderSize)
	buf := make([]byte, tcpReadBufferSize)

	for {
		if _, err := io.ReadFull(reader, header); err != nil {
			return
		}
		sampleID := binary.BigEndian.Uint32(header[0:4])
		payloadLen := binary.BigEndian.Uint32(header[4:8])
		if payloadLen == 0 {
			continue
		}
		if payloadLen > maxTCPFrameBytes {
			return
		}
		if int(payloadLen) > len(buf) {
			buf = make([]byte, payloadLen)
		}
		if _, err := io.ReadFull(reader, buf[:payloadLen]); err != nil {
			return
		}

		// Record into RPC session or legacy client
		if session != nil {
			session.RecordSample(time.Now(), sampleID, int(payloadLen), false)
		} else {
			legacyClient.recordSample(time.Now(), sampleID, int(payloadLen), false)
		}
	}
}

func handleTCPReverse(conn net.Conn, state *serverState, key string) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	reader := bufio.NewReader(conn)
	if sessionID, ok := peekSessionID(reader); ok {
		if session, found := state.sessionMgr.GetSession(sessionID); found {
			session.RegisterReverseConnection(tcpConn)
			_, _ = io.Copy(io.Discard, reader)
			session.ClearReverseConnection(tcpConn)
			return
		}
	}

	client := state.client(key)
	client.setReverseTCPConn(tcpConn)
	_, _ = io.Copy(io.Discard, reader)
	client.clearReverseTCPConn(tcpConn)
}

func handleControl(conn net.Conn, state *serverState, key string) {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		cmd, sampleID, args, err := parseControl(line)
		if err != nil {
			fmt.Fprintln(writer, "ERR", err.Error())
			if err := writer.Flush(); err != nil {
				return
			}
			continue
		}
		switch cmd {
		case "SAMPLE_START":
			if len(args) > 0 && strings.EqualFold(args[0], "REVERSE") {
				cfg, err := parseReverseArgs(args)
				if err != nil {
					fmt.Fprintln(writer, "ERR", err.Error())
					break
				}
				client := state.client(key)
				if cfg.udpPort > 0 {
					udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", key, cfg.udpPort))
					if err != nil {
						fmt.Fprintln(writer, "ERR invalid client address")
						break
					}
					if err := client.startReverseUDP(sampleID, state.udpConn, udpAddr, cfg); err != nil {
						fmt.Fprintln(writer, "ERR", err.Error())
						break
					}
				} else {
					if err := client.startReverseTCP(sampleID, cfg); err != nil {
						fmt.Fprintln(writer, "ERR", err.Error())
						break
					}
				}
				fmt.Fprintln(writer, "OK")
				break
			}
			state.client(key).startSample(sampleID)
			fmt.Fprintln(writer, "OK")
		case "SAMPLE_STOP":
			client := state.client(key)
			if client.isReverseActive() {
				report, err := client.stopReverse(sampleID)
				if err != nil {
					fmt.Fprintln(writer, "ERR", err.Error())
					break
				}
				payload, err := json.Marshal(report)
				if err != nil {
					fmt.Fprintln(writer, "ERR MARSHAL")
					break
				}
				fmt.Fprintln(writer, string(payload))
				break
			}
			if state.recvWait > 0 && client.shouldWait() {
				time.Sleep(state.recvWait)
			}
			report, err := client.stopSample(sampleID)
			if err != nil {
				fmt.Fprintln(writer, "ERR", err.Error())
				break
			}
			payload, err := json.Marshal(report)
			if err != nil {
				fmt.Fprintln(writer, "ERR MARSHAL")
				break
			}
			fmt.Fprintln(writer, string(payload))
		default:
			fmt.Fprintln(writer, "ERR UNKNOWN")
		}
		if err := writer.Flush(); err != nil {
			return
		}
	}
}

func parseControl(line string) (string, uint32, []string, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return "", 0, nil, errors.New("empty command")
	}
	cmd := strings.ToUpper(fields[0])
	if cmd != "SAMPLE_START" && cmd != "SAMPLE_STOP" {
		return cmd, 0, fields[1:], nil
	}
	if len(fields) < 2 {
		return "", 0, nil, errors.New("missing sample id")
	}
	id, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil {
		return "", 0, nil, fmt.Errorf("invalid sample id: %w", err)
	}
	return cmd, uint32(id), fields[2:], nil
}

func parseReverseArgs(args []string) (reverseConfig, error) {
	if len(args) < 6 {
		return reverseConfig{}, errors.New("reverse start requires: REVERSE <bandwidth_bps> <chunk_bytes> <rtt_ms> <sample_bytes> <udp_port>")
	}
	if !strings.EqualFold(args[0], "REVERSE") {
		return reverseConfig{}, errors.New("missing REVERSE directive")
	}

	bandwidth, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || bandwidth <= 0 {
		return reverseConfig{}, errors.New("invalid bandwidth")
	}
	chunkBytes, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil || chunkBytes <= 0 {
		return reverseConfig{}, errors.New("invalid chunk size")
	}
	rttMs, err := strconv.ParseInt(args[3], 10, 64)
	if err != nil || rttMs < 0 {
		return reverseConfig{}, errors.New("invalid rtt")
	}
	sampleBytes, err := strconv.ParseInt(args[4], 10, 64)
	if err != nil || sampleBytes <= 0 {
		return reverseConfig{}, errors.New("invalid sample bytes")
	}
	udpPort, err := strconv.Atoi(args[5])
	if err != nil || udpPort < 0 {
		return reverseConfig{}, errors.New("invalid udp port")
	}

	return reverseConfig{
		bandwidthBps: float64(bandwidth),
		chunkSize:    chunkBytes,
		rtt:          time.Duration(rttMs) * time.Millisecond,
		sampleBytes:  sampleBytes,
		udpPort:      udpPort,
	}, nil
}

func runUDP(conn *net.UDPConn, state *serverState) {
	readBufBytes := protocol.UDPMaxChunkSize
	if readBufBytes < protocol.UDPSeqHeaderSize+1 {
		readBufBytes = protocol.UDPSeqHeaderSize + 1
	}
	_ = conn.SetReadBuffer(readBufBytes * 4)
	buf := make([]byte, readBufBytes)

	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if n < 1 {
			continue
		}

		msgType := buf[0]
		switch msgType {
		case protocol.UDPTypeData:
			if n < protocol.UDPSeqHeaderSize {
				continue
			}
			sampleID := binary.BigEndian.Uint32(buf[1:5])
			seq := binary.BigEndian.Uint64(buf[5:13])
			payload := n - protocol.UDPSeqHeaderSize
			key := clientKey(addr)
			state.client(key).recordUDP(time.Now(), sampleID, payload, seq)
		case protocol.UDPTypeDataSession:
			if n < protocol.UDPSessionHeaderMin {
				continue
			}
			sessionLen := int(buf[1])
			if sessionLen <= 0 {
				continue
			}
			headerSize := protocol.UDPSessionHeaderMin + sessionLen
			if n < headerSize {
				continue
			}
			sessionID := string(buf[2 : 2+sessionLen])
			sampleOffset := 2 + sessionLen
			seqOffset := sampleOffset + 4
			sampleID := binary.BigEndian.Uint32(buf[sampleOffset:seqOffset])
			seq := binary.BigEndian.Uint64(buf[seqOffset : seqOffset+8])
			payload := n - headerSize
			if payload <= 0 {
				continue
			}
			if session, found := state.sessionMgr.GetSession(sessionID); found {
				session.RecordUDPPacket(time.Now(), sampleID, seq, payload)
			}
		case protocol.UDPTypePing:
			state.sessionMgr.RecordUDPPing(addr)
			resp := make([]byte, n)
			resp[0] = protocol.UDPTypePong
			copy(resp[1:], buf[1:n])
			_, _ = conn.WriteToUDP(resp, addr)
		default:
			continue
		}
	}
}

func peekSessionID(reader *bufio.Reader) (string, bool) {
	lenBuf, err := reader.Peek(2)
	if err != nil {
		return "", false
	}
	length := int(binary.BigEndian.Uint16(lenBuf))
	if length != 36 && length != 32 {
		return "", false
	}
	peek, err := reader.Peek(2 + length)
	if err != nil {
		return "", false
	}
	idBytes := peek[2:]
	if !looksLikeUUID(idBytes) {
		return "", false
	}
	if _, err := reader.Discard(2 + length); err != nil {
		return "", false
	}
	return string(idBytes), true
}

func looksLikeUUID(data []byte) bool {
	switch len(data) {
	case 36:
		for i, b := range data {
			switch i {
			case 8, 13, 18, 23:
				if b != '-' {
					return false
				}
			default:
				if !isHexByte(b) {
					return false
				}
			}
		}
		return true
	case 32:
		for _, b := range data {
			if !isHexByte(b) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isHexByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'f':
		return true
	case b >= 'A' && b <= 'F':
		return true
	default:
		return false
	}
}
