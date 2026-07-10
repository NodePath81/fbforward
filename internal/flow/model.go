package flow

import (
	"net/netip"
	"time"
)

const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

// Meta contains the immutable attributes assigned when a Flow is admitted.
type Meta struct {
	ID         ID
	Protocol   string
	ClientAddr netip.AddrPort
	Listener   string
	Route      string
	Upstream   string
	StartedAt  time.Time
}

// BackendTuple identifies the socket created by fbforward to reach an
// upstream. Addresses use fbforward's socket perspective: LocalAddr is the
// source endpoint seen by the backend and RemoteAddr is the backend endpoint.
type BackendTuple struct {
	Protocol   string
	BackendKey string
	LocalAddr  netip.AddrPort
	RemoteAddr netip.AddrPort
}

// Counters is a cumulative snapshot, not a delta.
type Counters struct {
	LastActivity time.Time
	BytesUp      uint64
	BytesDown    uint64
	SegmentsUp   uint64
	SegmentsDown uint64
}

// Summary is the single close record for one Flow.
type Summary struct {
	Meta
	EndedAt      time.Time
	LastActivity time.Time
	BytesUp      uint64
	BytesDown    uint64
	CloseReason  string
}

// Rejection represents an admission failure before a Flow exists.
type Rejection struct {
	Protocol         string
	ClientAddr       netip.AddrPort
	Listener         string
	Reason           string
	MatchedRuleType  string
	MatchedRuleValue string
	RecordedAt       time.Time
}
