//go:build linux

package fbmeasure

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

type tcpRetransTest struct {
	expectedBytes uint64
	connCh        chan net.Conn
}

func (c *Client) TCPRetrans(ctx context.Context, bytesToSend uint64) (RetransResult, error) {
	if bytesToSend == 0 {
		return RetransResult{}, fmt.Errorf("bytes must be > 0")
	}

	id, err := newTestID()
	if err != nil {
		return RetransResult{}, err
	}

	var result RetransResult
	req := tcpRetransRequest{
		TestID:    id.String(),
		Bytes:     bytesToSend,
		TimeoutMs: timeoutMillis(ctx, time.Second),
	}
	err = c.withLockedCall(ctx, opTCPRetrans, req, func() error {
		time.Sleep(defaultAuxStartDelay)

		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", c.remoteAddr)
		if err != nil {
			return err
		}
		defer conn.Close()

		preface := make([]byte, 0, len(tcpDataMarker)+testIDSize)
		preface = append(preface, []byte(tcpDataMarker)...)
		preface = append(preface, id[:]...)
		if _, err := conn.Write(preface); err != nil {
			return err
		}

		if deadline, ok := ctx.Deadline(); ok {
			if err := conn.SetDeadline(deadline); err != nil {
				return err
			}
		}
		if _, err := io.Copy(io.Discard, conn); err != nil {
			return err
		}
		return nil
	}, func(jsonPayload []byte) error {
		var resp tcpRetransResponse
		if err := unmarshalPayload(jsonPayload, &resp); err != nil {
			return err
		}
		if resp.TestID != id.String() {
			return fmt.Errorf("unexpected tcp_retrans test_id")
		}
		result = RetransResult{
			BytesSent:    resp.BytesSent,
			Retransmits:  resp.Retransmits,
			SegmentsSent: resp.SegmentsSent,
			RTT:          time.Duration(resp.RTTNs),
			RTTVar:       time.Duration(resp.RTTVarNs),
		}
		return nil
	})
	if err != nil {
		return RetransResult{}, err
	}
	return result, nil
}

func (s *Server) handleTCPRetrans(ctx context.Context, req tcpRetransRequest) (tcpRetransResponse, error) {
	if req.Bytes == 0 {
		return tcpRetransResponse{}, fmt.Errorf("bytes must be > 0")
	}
	id, err := parseTestID(req.TestID)
	if err != nil {
		return tcpRetransResponse{}, err
	}
	key := id.String()
	test := &tcpRetransTest{
		expectedBytes: req.Bytes,
		connCh:        make(chan net.Conn, 1),
	}

	s.mu.Lock()
	if _, exists := s.tcpRetransTests[key]; exists {
		s.mu.Unlock()
		return tcpRetransResponse{}, fmt.Errorf("duplicate tcp_retrans test_id")
	}
	s.tcpRetransTests[key] = test
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.tcpRetransTests, key)
		s.mu.Unlock()
	}()

	timeout := time.NewTimer(time.Duration(req.TimeoutMs) * time.Millisecond)
	defer timeout.Stop()

	var conn net.Conn
	select {
	case <-ctx.Done():
		return tcpRetransResponse{}, ctx.Err()
	case <-timeout.C:
		return tcpRetransResponse{}, fmt.Errorf("timed out waiting for data connection")
	case conn = <-test.connCh:
	}
	defer conn.Close()

	result, err := runTCPRetransmission(conn, req.Bytes)
	if err != nil {
		return tcpRetransResponse{}, err
	}
	return tcpRetransResponse{
		TestID:       key,
		BytesSent:    result.BytesSent,
		Retransmits:  result.Retransmits,
		SegmentsSent: result.SegmentsSent,
		RTTNs:        int64(result.RTT),
		RTTVarNs:     int64(result.RTTVar),
	}, nil
}

func (s *Server) handleTCPDataConn(ctx context.Context, conn net.Conn) {
	var rawID [testIDSize]byte
	if _, err := io.ReadFull(conn, rawID[:]); err != nil {
		return
	}
	id := TestID(rawID)
	key := id.String()

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		s.mu.Lock()
		test := s.tcpRetransTests[key]
		s.mu.Unlock()
		if test != nil {
			select {
			case test.connCh <- conn:
				return
			default:
				return
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runTCPRetransmission(conn net.Conn, bytesToSend uint64) (RetransResult, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return RetransResult{}, fmt.Errorf("unexpected data connection type %T", conn)
	}

	chunk := make([]byte, 32*1024)
	var sent uint64
	for sent < bytesToSend {
		remaining := bytesToSend - sent
		if remaining < uint64(len(chunk)) {
			chunk = chunk[:remaining]
		}
		n, err := tcpConn.Write(chunk)
		sent += uint64(n)
		if err != nil {
			return RetransResult{}, err
		}
	}

	_ = tcpConn.CloseWrite()
	time.Sleep(20 * time.Millisecond)

	stats, err := readTCPStats(tcpConn)
	if err != nil {
		return RetransResult{}, err
	}
	return RetransResult{
		BytesSent:    sent,
		Retransmits:  stats.Retransmits,
		SegmentsSent: stats.SegmentsSent,
		RTT:          stats.RTT,
		RTTVar:       stats.RTTVar,
	}, nil
}

type tcpStats struct {
	Retransmits  uint64
	SegmentsSent uint64
	RTT          time.Duration
	RTTVar       time.Duration
}

func readTCPStats(conn *net.TCPConn) (tcpStats, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return tcpStats{}, fmt.Errorf("syscall conn: %w", err)
	}

	var info *unix.TCPInfo
	var sockErr error
	if err := rawConn.Control(func(fd uintptr) {
		info, sockErr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	}); err != nil {
		return tcpStats{}, fmt.Errorf("control syscall: %w", err)
	}
	if sockErr != nil {
		return tcpStats{}, fmt.Errorf("getsockopt TCP_INFO: %w", sockErr)
	}
	if info == nil {
		return tcpStats{}, fmt.Errorf("getsockopt TCP_INFO: nil info")
	}
	return parseTCPInfo(info), nil
}

func parseTCPInfo(info *unix.TCPInfo) tcpStats {
	segmentsSent := uint64(info.Data_segs_out)
	if segmentsSent == 0 {
		segmentsSent = uint64(info.Segs_out)
	}
	if segmentsSent == 0 && info.Bytes_sent > 0 && info.Snd_mss > 0 {
		mss := uint64(info.Snd_mss)
		segmentsSent = (info.Bytes_sent + mss - 1) / mss
	}
	retransmits := uint64(info.Total_retrans)
	if retransmits == 0 && info.Bytes_retrans > 0 && info.Snd_mss > 0 {
		mss := uint64(info.Snd_mss)
		retransmits = (info.Bytes_retrans + mss - 1) / mss
	}
	return tcpStats{
		Retransmits:  retransmits,
		SegmentsSent: segmentsSent,
		RTT:          time.Duration(info.Rtt) * time.Microsecond,
		RTTVar:       time.Duration(info.Rttvar) * time.Microsecond,
	}
}
