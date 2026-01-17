package transport

import (
	"encoding/binary"
	"errors"
	"net"
	"time"

	"github.com/NodePath81/fbforward/bwprobe/internal/network"
	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
)

// ReverseRecordFunc records sent bytes for sample accounting.
type ReverseRecordFunc func(now time.Time, bytes int)

// StartTCPReverseSender starts a paced TCP send loop for reverse samples.
func StartTCPReverseSender(conn *net.TCPConn, sampleID uint32, payloadSize int, sampleBytes int64, bandwidthBps float64, rtt time.Duration, record ReverseRecordFunc, stopCh <-chan struct{}, doneCh chan<- struct{}) (int, error) {
	chunk := make([]byte, protocol.TCPFrameHeaderSize+payloadSize)
	for i := protocol.TCPFrameHeaderSize; i < len(chunk); i++ {
		chunk[i] = byte(i)
	}
	writeBuf := network.TCPWriteBufferBytes(bandwidthBps, rtt, payloadSize)
	_ = conn.SetNoDelay(true)
	_ = conn.SetWriteBuffer(writeBuf)
	_ = network.ApplyTCPPacingRate(conn, bandwidthBps)

	go func() {
		defer close(doneCh)
		var sent int64
		var lastDeadline time.Time
		for sent < sampleBytes {
			select {
			case <-stopCh:
				return
			default:
			}

			if time.Since(lastDeadline) > time.Second {
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				lastDeadline = time.Now()
			}

			binary.BigEndian.PutUint32(chunk[0:4], sampleID)
			binary.BigEndian.PutUint32(chunk[4:8], uint32(payloadSize))
			if err := writeFull(conn, chunk[:protocol.TCPFrameHeaderSize+payloadSize]); err != nil {
				return
			}
			sent += int64(payloadSize)
			if record != nil {
				record(time.Now(), payloadSize)
			}
		}
	}()

	return writeBuf, nil
}

// StartUDPReverseSender starts a paced UDP send loop for reverse samples.
func StartUDPReverseSender(conn *net.UDPConn, addr *net.UDPAddr, sampleID uint32, payloadSize int, sampleBytes int64, bandwidthBps float64, record ReverseRecordFunc, stopCh <-chan struct{}, doneCh chan<- struct{}) error {
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	packet := make([]byte, protocol.UDPSeqHeaderSize+len(payload))
	packet[0] = protocol.UDPTypeData
	copy(packet[protocol.UDPSeqHeaderSize:], payload)

	limiter := network.New(bandwidthBps / 8)
	done := make([]byte, protocol.UDPDoneHeaderSize)
	done[0] = protocol.UDPTypeDone
	binary.BigEndian.PutUint32(done[1:5], sampleID)

	go func() {
		defer close(doneCh)
		var sent int64
		var seq uint64
		for sent < sampleBytes {
			select {
			case <-stopCh:
				return
			default:
			}

			limiter.Wait(payloadSize)

			binary.BigEndian.PutUint32(packet[1:5], sampleID)
			binary.BigEndian.PutUint64(packet[5:13], seq)
			seq++
			frameSize := protocol.UDPSeqHeaderSize + payloadSize
			n, err := conn.WriteToUDP(packet[:frameSize], addr)
			if err != nil || n != frameSize {
				return
			}
			sent += int64(payloadSize)
			if record != nil {
				record(time.Now(), payloadSize)
			}
		}
		for i := 0; i < 3; i++ {
			_, _ = conn.WriteToUDP(done, addr)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	return nil
}

func writeFull(conn net.Conn, buf []byte) error {
	for len(buf) > 0 {
		n, err := conn.Write(buf)
		if err != nil {
			return err
		}
		if n <= 0 {
			return errors.New("short write")
		}
		buf = buf[n:]
	}
	return nil
}
