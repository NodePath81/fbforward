package fbmeasure

import (
	"time"
)

type Result struct {
	Protocol   Protocol
	Reachable  bool
	RTT        time.Duration
	ObservedAt time.Time
}
