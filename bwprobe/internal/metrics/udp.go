package metrics

import "sync"

// Receiver tracks UDP sequence numbers for loss estimation.
type Receiver struct {
	mu          sync.Mutex
	packetsRecv uint64
	bytesRecv   uint64
	initialized bool
	baseSeq     uint64
	maxSeq      uint64
}

// Add records a received packet sequence number.
func (r *Receiver) Add(seq uint64, bytes int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized {
		r.baseSeq = seq
		r.maxSeq = seq
		r.initialized = true
		r.packetsRecv++
		r.bytesRecv += uint64(bytes)
		return
	}

	if seq > r.maxSeq {
		r.maxSeq = seq
	}

	r.packetsRecv++
	r.bytesRecv += uint64(bytes)
}

// Stats returns current loss stats.
func (r *Receiver) Stats() (recv uint64, lost uint64, bytes uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.initialized {
		return 0, 0, 0
	}
	total := r.maxSeq - r.baseSeq + 1
	lost = 0
	if r.packetsRecv < total {
		lost = total - r.packetsRecv
	}
	return r.packetsRecv, lost, r.bytesRecv
}

// Reset clears stats.
func (r *Receiver) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.packetsRecv = 0
	r.bytesRecv = 0
	r.initialized = false
	r.baseSeq = 0
	r.maxSeq = 0
}
