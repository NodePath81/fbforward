//go:build linux

package metrics

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// TCPStats captures TCP metrics from TCP_INFO.
type TCPStats struct {
	// Loss metrics
	Retransmits  uint64
	SegmentsSent uint64

	// RTT metrics (from kernel's TCP stack)
	RTT    time.Duration // Smoothed RTT
	RTTVar time.Duration // RTT variance (jitter)

	// Additional useful metrics
	RTO time.Duration // Retransmission timeout
	ATO time.Duration // Predicted tick of soft clock for ACK
}

// ReadTCPStats reads TCP_INFO from a TCP connection and extracts key metrics.
func ReadTCPStats(conn *net.TCPConn) (TCPStats, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return TCPStats{}, fmt.Errorf("syscall conn: %w", err)
	}

	var info *unix.TCPInfo
	var sockErr error
	if err := rawConn.Control(func(fd uintptr) {
		info, sockErr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	}); err != nil {
		return TCPStats{}, fmt.Errorf("control syscall: %w", err)
	}
	if sockErr != nil {
		return TCPStats{}, fmt.Errorf("getsockopt TCP_INFO: %w", sockErr)
	}
	if info == nil {
		return TCPStats{}, fmt.Errorf("getsockopt TCP_INFO: nil info")
	}

	// Extract metrics from TCP_INFO.
	// Note: info.Rtt is in microseconds (scaled by 8 internally, but the field gives microseconds).
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
	return TCPStats{
		Retransmits:  retransmits,
		SegmentsSent: segmentsSent,
		RTT:          time.Duration(info.Rtt) * time.Microsecond,
		RTTVar:       time.Duration(info.Rttvar) * time.Microsecond,
		RTO:          time.Duration(info.Rto) * time.Microsecond,
		ATO:          time.Duration(info.Ato) * time.Microsecond,
	}, nil
}
