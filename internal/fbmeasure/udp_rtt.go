package fbmeasure

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type udpPingTest struct {
	expected int
	done     chan struct{}
	authKey  [udpAuthKeySize]byte

	mu        sync.Mutex
	received  int
	completed bool
}

func (t *udpPingTest) markReceived() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.received++
	if !t.completed && t.received >= t.expected {
		t.completed = true
		close(t.done)
	}
}

func (t *udpPingTest) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.received
}

func (c *Client) PingUDP(ctx context.Context, count int) (RTTStats, error) {
	if count <= 0 {
		return RTTStats{}, fmt.Errorf("count must be > 0")
	}

	id, err := newTestID()
	if err != nil {
		return RTTStats{}, err
	}

	addr, err := net.ResolveUDPAddr("udp", c.dialAddr)
	if err != nil {
		return RTTStats{}, err
	}

	udpConn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return RTTStats{}, err
	}
	defer udpConn.Close()

	authKey, err := newUDPAuthKey()
	if err != nil {
		return RTTStats{}, err
	}

	var stats RTTStats
	req := pingUDPRequest{
		TestID:    id.String(),
		AuthKey:   udpAuthKeyString(authKey),
		Count:     count,
		TimeoutMs: timeoutMillis(ctx, time.Second),
	}
	err = c.withLockedCall(ctx, opPingUDP, req, func() error {
		time.Sleep(defaultAuxStartDelay)
		var acc rttAccumulator
		for seq := 0; seq < count; seq++ {
			sentAt := time.Now()
			packet := make([]byte, 0, udpPingPacketSize)
			packet = append(packet, udpPacketKindPing)
			packet = appendTestID(packet, id)
			packet = binaryAppendUint64(packet, uint64(seq+1))
			packet = binaryAppendUint64(packet, uint64(sentAt.UnixNano()))
			packet = appendUDPAuthTag(packet, authKey)
			if deadline, ok := ctx.Deadline(); ok {
				if err := udpConn.SetDeadline(deadline); err != nil {
					return err
				}
			}
			if _, err := udpConn.Write(packet); err != nil {
				return err
			}
			buf := make([]byte, udpPingPacketSize)
			if _, err := io.ReadFull(udpConn, buf); err != nil {
				return err
			}
			if !verifyUDPAuthTag(buf, authKey) {
				return fmt.Errorf("invalid udp pong authenticator")
			}
			respID, seqNum, _, err := parseUDPPingPacket(buf)
			if err != nil {
				return err
			}
			if respID != id || seqNum != uint64(seq+1) || buf[0] != udpPacketKindPong {
				return fmt.Errorf("unexpected udp pong")
			}
			acc.Add(time.Since(sentAt))
		}
		stats = acc.Stats()
		return nil
	}, func(jsonPayload []byte) error {
		var resp pingUDPResponse
		if err := unmarshalPayload(jsonPayload, &resp); err != nil {
			return err
		}
		if resp.TestID != id.String() {
			return fmt.Errorf("unexpected ping_udp test_id")
		}
		if resp.Received < count {
			return fmt.Errorf("ping_udp received %d/%d", resp.Received, count)
		}
		return nil
	})
	if err != nil {
		return RTTStats{}, err
	}
	return stats, nil
}

func (s *Server) handlePingUDP(ctx context.Context, req pingUDPRequest) (pingUDPResponse, error) {
	if req.Count <= 0 {
		return pingUDPResponse{}, fmt.Errorf("count must be > 0")
	}
	id, err := parseTestID(req.TestID)
	if err != nil {
		return pingUDPResponse{}, err
	}
	authKey, err := parseUDPAuthKey(req.AuthKey)
	if err != nil {
		return pingUDPResponse{}, err
	}
	expected := req.Count
	if expected > maxPingCount {
		expected = maxPingCount
	}
	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 || timeoutMs > maxTimeoutMs {
		timeoutMs = maxTimeoutMs
	}
	test := &udpPingTest{
		expected: expected,
		done:     make(chan struct{}),
		authKey:  authKey,
	}
	key := id.String()

	s.mu.Lock()
	if _, exists := s.udpPingTests[key]; exists {
		s.mu.Unlock()
		return pingUDPResponse{}, fmt.Errorf("duplicate ping_udp test_id")
	}
	s.udpPingTests[key] = test
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.udpPingTests, key)
		s.mu.Unlock()
	}()

	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return pingUDPResponse{}, ctx.Err()
	case <-timer.C:
	case <-test.done:
	}

	return pingUDPResponse{
		TestID:   key,
		Received: test.count(),
	}, nil
}

func (s *Server) handleUDPPacket(udpConn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	if len(data) < 1+testIDSize {
		return
	}
	switch data[0] {
	case udpPacketKindPing:
		s.handleUDPPingPacket(udpConn, addr, data)
	case udpPacketKindLoss:
		s.handleUDPLossPacket(data)
	}
}

func (s *Server) handleUDPPingPacket(udpConn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	id, _, _, err := parseUDPPingPacket(data)
	if err != nil {
		return
	}
	key := id.String()

	s.mu.Lock()
	test := s.udpPingTests[key]
	s.mu.Unlock()
	if test == nil {
		return
	}
	if !verifyUDPAuthTag(data, test.authKey) {
		return
	}

	reply := append([]byte(nil), data[:udpPingHeaderSize]...)
	reply[0] = udpPacketKindPong
	reply = appendUDPAuthTag(reply, test.authKey)
	_, _ = udpConn.WriteToUDP(reply, addr)
	test.markReceived()
}

func parseUDPPingPacket(data []byte) (TestID, uint64, int64, error) {
	if len(data) < udpPingPacketSize {
		return TestID{}, 0, 0, fmt.Errorf("short udp ping packet")
	}
	var id TestID
	copy(id[:], data[1:1+testIDSize])
	seq := binary.BigEndian.Uint64(data[1+testIDSize : 1+testIDSize+8])
	ts := int64(binary.BigEndian.Uint64(data[1+testIDSize+8 : udpPingHeaderSize]))
	return id, seq, ts, nil
}

func binaryAppendUint64(dst []byte, value uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	return append(dst, buf[:]...)
}
