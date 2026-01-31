//go:build linux

package metrics

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestParseTCPInfoPrimary(t *testing.T) {
	info := &unix.TCPInfo{
		Data_segs_out:  100,
		Total_retrans:  5,
		Rtt:            20000,
		Rttvar:         5000,
		Rto:            30000,
		Ato:            1000,
		Bytes_sent:     140000, // should not be used when Data_segs_out set
		Bytes_retrans:  7000,
		Snd_mss:        1400,
		Segs_out:       0,
		Bytes_received: 0,
	}

	stats := parseTCPInfo(info)
	if stats.SegmentsSent != 100 || stats.Retransmits != 5 {
		t.Fatalf("expected segments 100 retrans 5, got %d %d", stats.SegmentsSent, stats.Retransmits)
	}
	if stats.RTT != 20*time.Millisecond || stats.RTTVar != 5*time.Millisecond || stats.RTO != 30*time.Millisecond {
		t.Fatalf("unexpected timing fields: %+v", stats)
	}
	if stats.ATO != time.Millisecond {
		t.Fatalf("unexpected ATO: %v", stats.ATO)
	}
}

func TestParseTCPInfoFallbacks(t *testing.T) {
	info := &unix.TCPInfo{
		Data_segs_out: 0,
		Segs_out:      0,
		Bytes_sent:    140000,
		Bytes_retrans: 7000,
		Snd_mss:       1400,
		Rtt:           1000,
	}

	stats := parseTCPInfo(info)
	if stats.SegmentsSent != 100 {
		t.Fatalf("fallback segments expected 100, got %d", stats.SegmentsSent)
	}
	if stats.Retransmits != 5 {
		t.Fatalf("fallback retrans expected 5, got %d", stats.Retransmits)
	}
}

func TestParseTCPInfoZero(t *testing.T) {
	info := &unix.TCPInfo{}
	stats := parseTCPInfo(info)
	if stats.SegmentsSent != 0 || stats.Retransmits != 0 {
		t.Fatalf("expected zeros, got %+v", stats)
	}
}
