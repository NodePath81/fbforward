package fbmeasure

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

type udpLossReceiver struct {
	mu          sync.Mutex
	initialized bool
	baseSeq     uint64
	maxSeq      uint64
	packetsRecv uint64
	outOfOrder  uint64
	seen        map[uint64]struct{}
}

func newUDPLossReceiver() *udpLossReceiver {
	return &udpLossReceiver{
		seen: make(map[uint64]struct{}),
	}
}

func (r *udpLossReceiver) Add(seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.seen[seq]; exists {
		return
	}
	r.seen[seq] = struct{}{}
	if !r.initialized {
		r.initialized = true
		r.baseSeq = seq
		r.maxSeq = seq
		r.packetsRecv = 1
		return
	}
	if seq < r.maxSeq {
		r.outOfOrder++
	}
	if seq > r.maxSeq {
		r.maxSeq = seq
	}
	if seq < r.baseSeq {
		r.baseSeq = seq
	}
	r.packetsRecv++
}

func (r *udpLossReceiver) Stats(expected uint64) (recv uint64, lost uint64, outOfOrder uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	recv = r.packetsRecv
	outOfOrder = r.outOfOrder
	if expected > recv {
		lost = expected - recv
	}
	return recv, lost, outOfOrder
}

type udpLossTest struct {
	expected uint64
	receiver *udpLossReceiver
	notify   chan struct{}
}

func (c *Client) UDPLoss(ctx context.Context, packets, packetSize int) (LossResult, error) {
	if packets <= 0 {
		return LossResult{}, fmt.Errorf("packets must be > 0")
	}
	if packetSize < udpLossHeaderSize {
		return LossResult{}, fmt.Errorf("packet_size must be >= %d", udpLossHeaderSize)
	}

	id, err := newTestID()
	if err != nil {
		return LossResult{}, err
	}
	addr, err := net.ResolveUDPAddr("udp", c.remoteAddr)
	if err != nil {
		return LossResult{}, err
	}
	udpConn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return LossResult{}, err
	}
	defer udpConn.Close()

	var result LossResult
	req := udpLossRequest{
		TestID:     id.String(),
		Packets:    packets,
		PacketSize: packetSize,
		TimeoutMs:  timeoutMillis(ctx, time.Second),
	}
	err = c.withLockedCall(ctx, opUDPLoss, req, func() error {
		time.Sleep(defaultAuxStartDelay)
		packet := make([]byte, packetSize)
		packet[0] = udpPacketKindLoss
		copy(packet[1:1+testIDSize], id[:])
		for seq := 0; seq < packets; seq++ {
			binary.BigEndian.PutUint64(packet[1+testIDSize:udpLossHeaderSize], uint64(seq+1))
			if _, err := udpConn.Write(packet); err != nil {
				return err
			}
		}
		return nil
	}, func(jsonPayload []byte) error {
		var resp udpLossResponse
		if err := unmarshalPayload(jsonPayload, &resp); err != nil {
			return err
		}
		if resp.TestID != id.String() {
			return fmt.Errorf("unexpected udp_loss test_id")
		}
		result = LossResult{
			PacketsSent: resp.PacketsSent,
			PacketsRecv: resp.PacketsRecv,
			PacketsLost: resp.PacketsLost,
			OutOfOrder:  resp.OutOfOrder,
			LossRate:    resp.LossRate,
		}
		return nil
	})
	if err != nil {
		return LossResult{}, err
	}
	return result, nil
}

func (s *Server) handleUDPLoss(ctx context.Context, req udpLossRequest) (udpLossResponse, error) {
	if req.Packets <= 0 {
		return udpLossResponse{}, fmt.Errorf("packets must be > 0")
	}
	if req.PacketSize < udpLossHeaderSize {
		return udpLossResponse{}, fmt.Errorf("packet_size must be >= %d", udpLossHeaderSize)
	}
	id, err := parseTestID(req.TestID)
	if err != nil {
		return udpLossResponse{}, err
	}
	key := id.String()
	test := &udpLossTest{
		expected: uint64(req.Packets),
		receiver: newUDPLossReceiver(),
		notify:   make(chan struct{}, 1),
	}

	s.mu.Lock()
	if _, exists := s.udpLossTests[key]; exists {
		s.mu.Unlock()
		return udpLossResponse{}, fmt.Errorf("duplicate udp_loss test_id")
	}
	s.udpLossTests[key] = test
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.udpLossTests, key)
		s.mu.Unlock()
	}()

	overallTimer := time.NewTimer(time.Duration(req.TimeoutMs) * time.Millisecond)
	defer overallTimer.Stop()
	idleTimer := time.NewTimer(s.cfg.UDPReceiveWait)
	defer idleTimer.Stop()

	for {
		recv, _, _ := test.receiver.Stats(test.expected)
		if recv >= test.expected {
			break
		}
		select {
		case <-ctx.Done():
			return udpLossResponse{}, ctx.Err()
		case <-overallTimer.C:
			goto finish
		case <-idleTimer.C:
			goto finish
		case <-test.notify:
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(s.cfg.UDPReceiveWait)
		}
	}

finish:
	recv, lost, outOfOrder := test.receiver.Stats(test.expected)
	resp := udpLossResponse{
		TestID:      key,
		PacketsSent: test.expected,
		PacketsRecv: recv,
		PacketsLost: lost,
		OutOfOrder:  outOfOrder,
	}
	if resp.PacketsSent > 0 {
		resp.LossRate = float64(resp.PacketsLost) / float64(resp.PacketsSent)
	}
	return resp, nil
}

func (s *Server) handleUDPLossPacket(data []byte) {
	id, seq, err := parseUDPLossPacket(data)
	if err != nil {
		return
	}
	key := id.String()

	s.mu.Lock()
	test := s.udpLossTests[key]
	s.mu.Unlock()
	if test == nil {
		return
	}

	test.receiver.Add(seq)
	select {
	case test.notify <- struct{}{}:
	default:
	}
}

func parseUDPLossPacket(data []byte) (TestID, uint64, error) {
	if len(data) < udpLossHeaderSize {
		return TestID{}, 0, fmt.Errorf("short udp loss packet")
	}
	var id TestID
	copy(id[:], data[1:1+testIDSize])
	seq := binary.BigEndian.Uint64(data[1+testIDSize : udpLossHeaderSize])
	return id, seq, nil
}
