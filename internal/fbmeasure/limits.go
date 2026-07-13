package fbmeasure

import "time"

const (
	maxControlMessageSize = 1 << 16

	maxConnections   = 50
	maxConnsPerIP    = 10
	maxTimeoutMs     = 30_000
	maxPingCount     = 1000
	handshakeTimeout = 5 * time.Second
)
