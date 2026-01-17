package network

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/metrics"
	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"

	"golang.org/x/sys/unix"
)

type SendStats struct {
	TCP *metrics.TCPStats
	UDP *UDPStats
}

type UDPStats struct {
	Recv        uint64
	Lost        uint64
	Bytes       uint64
	PacketsSent uint64
}

// Sender interface abstracts TCP and UDP sending.
type Sender interface {
	Send(remaining int64) (int, error)
	SetSampleID(sampleID uint32)
	Close() error
	Stats() (SendStats, error)
}

// tcpSender handles TCP sending with optimized buffering.
type tcpSender struct {
	conn         *net.TCPConn
	chunk        []byte
	payloadSize  int
	sampleID     uint32
	lastDeadline time.Time
}

func NewTCPSender(target string, port int, bandwidthBps float64, rtt time.Duration, chunkSize int64) (*tcpSender, error) {
	return NewTCPSenderWithSession(target, port, bandwidthBps, rtt, chunkSize, "")
}

func NewTCPSenderWithSession(target string, port int, bandwidthBps float64, rtt time.Duration, chunkSize int64, sessionID string) (*tcpSender, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", target, port), 3*time.Second)
	if err != nil {
		return nil, err
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		conn.Close()
		return nil, errors.New("connection is not TCP")
	}

	payloadSize, err := TCPPayloadSize(chunkSize)
	if err != nil {
		_ = tcpConn.Close()
		return nil, err
	}
	chunk := make([]byte, protocol.TCPFrameHeaderSize+payloadSize)
	for i := protocol.TCPFrameHeaderSize; i < len(chunk); i++ {
		chunk[i] = byte(i)
	}

	_ = tcpConn.SetNoDelay(true)
	_ = tcpConn.SetWriteBuffer(TCPWriteBufferBytes(bandwidthBps, rtt, payloadSize))

	if err := setTCPPacingRate(tcpConn, bandwidthBps); err != nil {
		_ = tcpConn.Close()
		return nil, err
	}

	if _, err := tcpConn.Write([]byte(protocol.TCPDataHeader)); err != nil {
		_ = tcpConn.Close()
		return nil, err
	}

	// Send session ID if using RPC protocol
	if sessionID != "" {
		// Send length-prefixed session ID
		sessionBytes := []byte(sessionID)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(sessionBytes)))
		if _, err := tcpConn.Write(lenBuf); err != nil {
			_ = tcpConn.Close()
			return nil, err
		}
		if _, err := tcpConn.Write(sessionBytes); err != nil {
			_ = tcpConn.Close()
			return nil, err
		}
	}

	return &tcpSender{
		conn:        tcpConn,
		chunk:       chunk,
		payloadSize: payloadSize,
	}, nil
}

func (s *tcpSender) Send(remaining int64) (int, error) {
	if remaining <= 0 {
		return 0, errors.New("short write")
	}
	payloadLen := s.payloadSize

	if time.Since(s.lastDeadline) > time.Second {
		_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		s.lastDeadline = time.Now()
	}

	binary.BigEndian.PutUint32(s.chunk[0:4], s.sampleID)
	binary.BigEndian.PutUint32(s.chunk[4:8], uint32(payloadLen))
	if err := writeFull(s.conn, s.chunk[:protocol.TCPFrameHeaderSize+payloadLen]); err != nil {
		return 0, err
	}
	return payloadLen, nil
}

func (s *tcpSender) SetSampleID(sampleID uint32) {
	s.sampleID = sampleID
}

func (s *tcpSender) Close() error {
	return s.conn.Close()
}

func (s *tcpSender) Stats() (SendStats, error) {
	info, err := metrics.ReadTCPStats(s.conn)
	if err != nil {
		return SendStats{}, err
	}
	return SendStats{TCP: &info}, nil
}

func TCPWriteBufferBytes(bandwidthBps float64, rtt time.Duration, payloadSize int) int {
	if rtt > 0 && bandwidthBps > 0 {
		rateBytes := bandwidthBps / 8
		bdp := int(math.Ceil(rateBytes * rtt.Seconds()))
		if bdp > 0 {
			return bdp
		}
	}
	return payloadSize + protocol.TCPFrameHeaderSize
}

func TCPPayloadSize(chunkSize int64) (int, error) {
	if chunkSize <= protocol.TCPFrameHeaderSize {
		return 0, fmt.Errorf("chunk-size must be > %d for tcp", protocol.TCPFrameHeaderSize)
	}
	if chunkSize > math.MaxInt32 {
		return 0, errors.New("chunk-size too large")
	}
	return int(chunkSize) - protocol.TCPFrameHeaderSize, nil
}

func setTCPPacingRate(conn *net.TCPConn, bandwidthBps float64) error {
	if bandwidthBps <= 0 {
		return nil
	}
	rateBytes := bandwidthBps / 8
	if rateBytes < 1 {
		rateBytes = 1
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	controlErr := raw.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptUint64(int(fd), unix.SOL_SOCKET, unix.SO_MAX_PACING_RATE, uint64(rateBytes))
	})
	if controlErr != nil {
		return controlErr
	}
	return sockErr
}

// ApplyTCPPacingRate applies SO_MAX_PACING_RATE to a TCP connection.
func ApplyTCPPacingRate(conn *net.TCPConn, bandwidthBps float64) error {
	if conn == nil {
		return errors.New("nil tcp connection")
	}
	return setTCPPacingRate(conn, bandwidthBps)
}

// udpSender handles UDP sending with sequence tracking.
type udpSender struct {
	conn         *net.UDPConn
	limiter      *Limiter
	payload      []byte
	packet       []byte
	seq          uint64
	packetsSent  uint64
	batchBytes   int64
	sampleID     uint32
	lastDeadline time.Time
	headerSize   int
	sampleOffset int
	seqOffset    int
	payloadSize  int
}

func NewUDPSender(target string, port int, limiter *Limiter, sampleBytes int64, chunkSize int64) (*udpSender, error) {
	return NewUDPSenderWithSession(target, port, limiter, sampleBytes, chunkSize, "")
}

func NewUDPSenderWithSession(target string, port int, limiter *Limiter, sampleBytes int64, chunkSize int64, sessionID string) (*udpSender, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", target, port))
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}

	_ = conn.SetWriteBuffer(4 * 1024 * 1024)

	headerSize, sampleOffset, seqOffset, err := udpHeaderLayout(sessionID)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	payloadSize, _, err := UDPPayloadSizeWithHeader(chunkSize, headerSize)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	packet := make([]byte, headerSize+len(payload))
	packet[0] = protocol.UDPTypeData
	if sessionID != "" {
		packet[0] = protocol.UDPTypeDataSession
		packet[1] = byte(len(sessionID))
		copy(packet[2:2+len(sessionID)], []byte(sessionID))
	}
	copy(packet[headerSize:], payload)

	batchBytes := int64(64 * 1024)
	if batchBytes > sampleBytes {
		batchBytes = sampleBytes
	}

	return &udpSender{
		conn:         conn,
		limiter:      limiter,
		payload:      payload,
		packet:       packet,
		batchBytes:   batchBytes,
		headerSize:   headerSize,
		sampleOffset: sampleOffset,
		seqOffset:    seqOffset,
		payloadSize:  payloadSize,
	}, nil
}

func (s *udpSender) Send(remaining int64) (int, error) {
	if remaining <= 0 {
		return 0, errors.New("udp sample bytes too small")
	}

	payloadSize := s.payloadSize
	s.limiter.Wait(payloadSize)

	if time.Since(s.lastDeadline) > time.Second {
		_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		s.lastDeadline = time.Now()
	}

	binary.BigEndian.PutUint32(s.packet[s.sampleOffset:s.sampleOffset+4], s.sampleID)
	binary.BigEndian.PutUint64(s.packet[s.seqOffset:s.seqOffset+8], s.seq)
	frameSize := s.headerSize + payloadSize
	n, err := s.conn.Write(s.packet[:frameSize])
	if n == 0 && err == nil {
		return 0, errors.New("short udp write")
	}
	if err != nil {
		return 0, err
	}
	if n < frameSize {
		return 0, errors.New("partial udp write")
	}
	s.packetsSent++
	s.seq++

	return payloadSize, nil
}

func (s *udpSender) SetSampleID(sampleID uint32) {
	s.sampleID = sampleID
}

func (s *udpSender) Close() error {
	return s.conn.Close()
}

func (s *udpSender) Stats() (SendStats, error) {
	return SendStats{UDP: &UDPStats{PacketsSent: s.packetsSent}}, nil
}

func UDPPayloadSize(chunkSize int64) (int, int, error) {
	return UDPPayloadSizeWithHeader(chunkSize, protocol.UDPSeqHeaderSize)
}

func UDPPayloadSizeWithHeader(chunkSize int64, headerSize int) (int, int, error) {
	if chunkSize <= int64(headerSize) {
		return 0, 0, fmt.Errorf("chunk-size must be > %d for udp", headerSize)
	}
	total := int(chunkSize)
	if total > protocol.UDPMaxChunkSize {
		total = protocol.UDPMaxChunkSize
	}
	payloadSize := total - headerSize
	if payloadSize <= 0 {
		return 0, 0, errors.New("chunk-size too small for udp")
	}
	return payloadSize, total, nil
}

func udpHeaderLayout(sessionID string) (headerSize int, sampleOffset int, seqOffset int, err error) {
	if sessionID == "" {
		return protocol.UDPSeqHeaderSize, 1, 5, nil
	}
	if len(sessionID) > 255 {
		return 0, 0, 0, errors.New("session id too long for udp")
	}
	headerSize = protocol.UDPSessionHeaderMin + len(sessionID)
	sampleOffset = 2 + len(sessionID)
	seqOffset = sampleOffset + 4
	return headerSize, sampleOffset, seqOffset, nil
}

func writeFull(conn net.Conn, buf []byte) error {
	for len(buf) > 0 {
		n, err := conn.Write(buf)
		if n > 0 {
			buf = buf[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("short write")
		}
	}
	return nil
}
