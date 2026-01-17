package transport

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/metrics"
	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
)

const maxTCPFrameBytes = 4 * 1024 * 1024

// Receiver abstracts data reception for a sample.
type Receiver interface {
	Receive(remaining int64) (int, error)
	SetSampleID(sampleID uint32)
	Close() error
}

// SampleStatsProvider exposes per-sample receive stats.
type SampleStatsProvider interface {
	SampleStats() (recv uint64, lost uint64, bytes uint64)
}

// TCPReceiver reads framed TCP data streams.
type TCPReceiver struct {
	conn         *net.TCPConn
	sampleID     uint32
	readTimeout  time.Duration
	lastDeadline time.Time
	header       []byte
	buf          []byte
}

func NewTCPReceiver(conn *net.TCPConn, readTimeout time.Duration) *TCPReceiver {
	return &TCPReceiver{
		conn:        conn,
		readTimeout: readTimeout,
		header:      make([]byte, protocol.TCPFrameHeaderSize),
		buf:         make([]byte, 256*1024),
	}
}

func (r *TCPReceiver) SetSampleID(sampleID uint32) {
	r.sampleID = sampleID
}

func (r *TCPReceiver) Receive(remaining int64) (int, error) {
	for {
		if time.Since(r.lastDeadline) > time.Second {
			_ = r.conn.SetReadDeadline(time.Now().Add(r.readTimeout))
			r.lastDeadline = time.Now()
		}
		if _, err := io.ReadFull(r.conn, r.header); err != nil {
			return 0, err
		}
		sampleID := binary.BigEndian.Uint32(r.header[0:4])
		payloadLen := binary.BigEndian.Uint32(r.header[4:8])
		if payloadLen == 0 {
			continue
		}
		if payloadLen > maxTCPFrameBytes {
			return 0, errors.New("tcp frame too large")
		}
		if int(payloadLen) > len(r.buf) {
			r.buf = make([]byte, payloadLen)
		}
		if _, err := io.ReadFull(r.conn, r.buf[:payloadLen]); err != nil {
			return 0, err
		}
		if sampleID != r.sampleID {
			continue
		}
		return int(payloadLen), nil
	}
}

func (r *TCPReceiver) Close() error {
	return r.conn.Close()
}

// UDPReceiver reads UDP packets and tracks loss.
type UDPReceiver struct {
	conn        *net.UDPConn
	sampleID    uint32
	readTimeout time.Duration
	receiver    metrics.Receiver
	buf         []byte
}

func NewUDPReceiver(conn *net.UDPConn, readTimeout time.Duration) *UDPReceiver {
	return &UDPReceiver{
		conn:        conn,
		readTimeout: readTimeout,
		buf:         make([]byte, protocol.UDPMaxChunkSize),
	}
}

func (r *UDPReceiver) SetSampleID(sampleID uint32) {
	r.sampleID = sampleID
	r.receiver.Reset()
}

func (r *UDPReceiver) Receive(remaining int64) (int, error) {
	for {
		if err := r.conn.SetReadDeadline(time.Now().Add(r.readTimeout)); err != nil {
			return 0, err
		}
		n, _, err := r.conn.ReadFromUDP(r.buf)
		if err != nil {
			return 0, err
		}
		if n < 1 {
			continue
		}
		if r.buf[0] == protocol.UDPTypeDone && n >= protocol.UDPDoneHeaderSize {
			doneSampleID := binary.BigEndian.Uint32(r.buf[1:5])
			if doneSampleID == r.sampleID {
				return 0, io.EOF
			}
			continue
		}
		if r.buf[0] != protocol.UDPTypeData {
			continue
		}
		if n < protocol.UDPSeqHeaderSize {
			continue
		}
		frameSampleID := binary.BigEndian.Uint32(r.buf[1:5])
		if frameSampleID != r.sampleID {
			continue
		}
		seq := binary.BigEndian.Uint64(r.buf[5:13])
		payload := n - protocol.UDPSeqHeaderSize
		if payload <= 0 {
			continue
		}
		r.receiver.Add(seq, payload)
		return payload, nil
	}
}

func (r *UDPReceiver) SampleStats() (recv uint64, lost uint64, bytes uint64) {
	return r.receiver.Stats()
}

func (r *UDPReceiver) Close() error {
	return r.conn.Close()
}
