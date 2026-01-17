# Code Review: bwprobe - Actionable Tasks

**Review Date**: 2026-01-17
**Previous Review**: [task-log/code-review-1.md](task-log/code-review-1.md)

## Overview

5 critical bugs + 5 code quality issues identified. This document provides concrete fixes for each issue.

**Stats**: 34 Go files, ~7500 LOC

---

## Critical Bug Fixes

### 1. Fix RTTSampler Double-Stop Panic

**File**: [internal/metrics/rtt_sampler.go](internal/metrics/rtt_sampler.go)

**Task**: Make `Stop()` idempotent to prevent panic on double-call.

**Current Code** (line 73-76):
```go
func (s *RTTSampler) Stop() {
    close(s.stopCh)  // ⚠️ panic if called twice
    <-s.doneCh
}
```

**Change Required**:

1. Add `stopOnce` field to `RTTSampler` struct (line 19):
```go
type RTTSampler struct {
    rate     int
    mu       sync.Mutex
    stopOnce sync.Once  // ADD THIS
    count    int
    mean     float64
    m2       float64
    min      time.Duration
    max      time.Duration
    stopCh   chan struct{}
    doneCh   chan struct{}
}
```

2. Update `Stop()` method:
```go
func (s *RTTSampler) Stop() {
    s.stopOnce.Do(func() {
        close(s.stopCh)
    })
    <-s.doneCh
}
```

**Test**: Verify by calling `Stop()` twice - should not panic.

---

### 2. Fix Legacy Client State Collision

**File**: [internal/server/server.go](internal/server/server.go)

**Task**: Fix `clientKey()` to include port, preventing state collision between clients from same IP.

**Current Code** (line 410-416):
```go
func clientKey(addr net.Addr) string {
    host, _, err := net.SplitHostPort(addr.String())
    if err != nil {
        return addr.String()
    }
    return host  // ⚠️ Port discarded
}
```

**Change Required**:
```go
func clientKey(addr net.Addr) string {
    return addr.String()  // Keep IP:port
}
```

**Test**: Run two clients from same IP on different ports - metrics should not mix.

---

### 3. Add RPC Call Timeouts

**File**: [internal/rpc/client.go](internal/rpc/client.go)

**Task**: Add connection deadlines to prevent indefinite hangs.

**Current Code** (line 67-136 in `call()` method - no timeouts):
```go
func (c *RPCClient) call(method string, params interface{}, result interface{}) error {
    c.mu.Lock()
    id := c.nextID
    c.nextID++
    c.mu.Unlock()
    // ... rest of function
}
```

**Change Required**:

1. Add timeout constant at top of file:
```go
const defaultRPCTimeout = 10 * time.Second
```

2. Add deadline setting in `call()` method after acquiring lock:
```go
func (c *RPCClient) call(method string, params interface{}, result interface{}) error {
    c.mu.Lock()

    // ADD THESE LINES
    deadline := time.Now().Add(defaultRPCTimeout)
    c.conn.SetDeadline(deadline)
    defer c.conn.SetDeadline(time.Time{})

    id := c.nextID
    c.nextID++

    // ... rest of function unchanged
}
```

**Alternative** (better): Make timeout configurable via `RPCClient` field.

**Test**: Simulate server hang - client should timeout after 10s.

---

### 4. Fix Reverse TCP Download Infinite Retry

**File**: [internal/engine/samples.go](internal/engine/samples.go)

**Task**: Add per-sample deadline and timeout counter to prevent infinite retry loops.

**Current Code** (line 75-107):
```go
var deadline time.Time
if direction == DirectionDownload && strings.ToLower(cfg.Network) == "udp" {
    // UDP has deadline, TCP doesn't
    expectedDur := time.Duration(...)
    deadline = time.Now().Add(expectedDur + 2*reverseReadTimeout)
}

for sampleBytes < cfg.SampleBytes {
    // ...
    n, err := exec.Transfer(remaining)
    if err != nil {
        if errors.Is(err, io.EOF) {
            break
        }
        if ne, ok := err.(net.Error); ok && ne.Timeout() {
            continue  // ⚠️ Infinite loop
        }
        return result, err
    }
    // ...
}
```

**Change Required**:

1. **Extend deadline logic to TCP** (line ~75):
```go
var deadline time.Time
if direction == DirectionDownload {  // Remove UDP-only check
    expectedDur := time.Duration(0)
    if cfg.BandwidthBps > 0 {
        expectedDur = time.Duration(float64(cfg.SampleBytes*8) / cfg.BandwidthBps * float64(time.Second))
    }
    if expectedDur <= 0 {
        expectedDur = 10 * time.Second
    }
    if strings.ToLower(cfg.Network) == "udp" {
        deadline = time.Now().Add(expectedDur + 2*reverseReadTimeout)
    } else {
        deadline = time.Now().Add(expectedDur + 5*time.Second)
    }
}
```

2. **Add timeout counter** (before the for loop at line ~86):
```go
timeoutCount := 0
const maxTimeouts = 10

for sampleBytes < cfg.SampleBytes {
    // Add deadline check at top of loop
    if !deadline.IsZero() && time.Now().After(deadline) {
        return result, fmt.Errorf("sample deadline exceeded after %v", time.Since(start))
    }

    // ... existing ctx and MaxDuration checks ...

    n, err := exec.Transfer(remaining)
    if err != nil {
        if errors.Is(err, io.EOF) {
            break
        }
        if ne, ok := err.(net.Error); ok && ne.Timeout() {
            timeoutCount++
            if timeoutCount >= maxTimeouts {
                return result, fmt.Errorf("too many timeouts (%d): %w", maxTimeouts, err)
            }
            continue
        }
        return result, err
    }
    // ... rest of loop
}
```

**Test**: Simulate server that never sends data - client should timeout, not hang forever.

---

### 5. Fix UDP Ping Map Memory Leak

**Files**: [internal/rpc/session.go](internal/rpc/session.go), [internal/server/server.go](internal/server/server.go)

**Task**: Add cleanup for `udpPing` map to prevent unbounded growth.

**Changes Required**:

1. **Add cleanup method in session.go** (after line 206):
```go
// CleanupExpiredUDPPings removes UDP ping records older than maxAge
func (sm *SessionManager) CleanupExpiredUDPPings(maxAge time.Duration) int {
    sm.udpPingMu.Lock()
    defer sm.udpPingMu.Unlock()

    now := time.Now()
    count := 0
    for key, ts := range sm.udpPing {
        if now.Sub(ts) > maxAge {
            delete(sm.udpPing, key)
            count++
        }
    }
    return count
}
```

2. **Call cleanup in server goroutine** (modify existing cleanup at line 447-457):
```go
go func() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for range ticker.C {
        cleaned := state.sessionMgr.CleanupExpiredSessions(60 * time.Second)
        pingsCleaned := state.sessionMgr.CleanupExpiredUDPPings(15 * time.Minute)  // ADD THIS
        if cleaned > 0 || pingsCleaned > 0 {
            fmt.Printf("Cleaned up %d sessions, %d UDP pings\n", cleaned, pingsCleaned)
        }
    }
}()
```

**Test**: Long-running server - UDP ping map should not grow unbounded.

---

## Code Quality Improvements

### 6. Add Network Validation in Sampler

**File**: [probe/sampler.go](probe/sampler.go)

**Task**: Validate network parameter to prevent silent fallback to UDP.

**Add after line 63** (in `Connect()` method):
```go
networkName := strings.ToLower(strings.TrimSpace(s.config.Network))
if networkName == "" {
    networkName = DefaultNetwork
}

// ADD THIS VALIDATION
if networkName != "tcp" && networkName != "udp" {
    return fmt.Errorf("invalid network %q (must be tcp or udp)", networkName)
}
```

---

### 7. Remove Duplicate Progress Code

**Files**: [internal/engine/progress.go](internal/engine/progress.go), [internal/progress/progress.go](internal/progress/progress.go)

**Task**: Investigate which progress implementation is used, then remove or document the duplicate.

**Steps**:
1. Search for usage of `NewProgressTracker`:
   ```bash
   grep -r "NewProgressTracker" --include="*.go" .
   ```

2. Search for usage of `progress.Bar` or `progress.New`:
   ```bash
   grep -r "progress\.Bar\|progress\.New" --include="*.go" .
   ```

3. **If `internal/engine/progress.go` is unused**: Delete the file

4. **If both are used**: Add comment explaining why both exist

---

### 8. Make Inter-Sample Wait Cancellable

**File**: [internal/engine/samples.go](internal/engine/samples.go)

**Task**: Replace `time.Sleep` with context-aware wait.

**Current Code** (line 156-158):
```go
if sample < cfg.Samples-1 && cfg.Wait > 0 {
    time.Sleep(cfg.Wait)
}
```

**Change Required**:
```go
if sample < cfg.Samples-1 && cfg.Wait > 0 {
    select {
    case <-ctx.Done():
        return result, ctx.Err()
    case <-time.After(cfg.Wait):
    }
}
```

---

### 9. Extract Magic Numbers to Constants

**Files**: Multiple

**Task**: Replace hardcoded values with named constants.

**In [internal/rpc/session.go](internal/rpc/session.go)** (add at top):
```go
const (
    defaultIntervalDuration = 100 * time.Millisecond
    sessionCleanupInterval  = 30 * time.Second
    sessionTimeout          = 60 * time.Second
    udpPingMaxAge           = 15 * time.Minute
)
```

**In [internal/server/server.go](internal/server/server.go)** (add at top):
```go
const (
    intervalDuration = 100 * time.Millisecond
    maxTCPFrameBytes = 4 * 1024 * 1024
    maxRPCMessageSize = 10 * 1024 * 1024
    tcpReadBufferSize = 256 * 1024
)
```

Then replace all instances of these values with the constants.

---

### 10. Replace Busy-Wait Loops with Channels

**Files**: [internal/server/server.go](internal/server/server.go), [internal/rpc/session.go](internal/rpc/session.go)

**Task**: Replace 10ms polling loops with channel notifications.

**In both files, modify `clientState` / `SessionState` struct**:

Add field:
```go
type clientState struct {
    // ... existing fields ...
    reverseConnReady chan struct{}
}
```

**Update `setReverseTCPConn()` method**:
```go
func (c *clientState) setReverseTCPConn(conn *net.TCPConn) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.reverseTCP != nil && c.reverseTCP != conn {
        _ = c.reverseTCP.Close()
    }
    c.reverseTCP = conn

    // Signal connection ready
    if c.reverseConnReady != nil {
        close(c.reverseConnReady)
        c.reverseConnReady = nil
    }
}
```

**Replace `waitReverseTCP()` method**:
```go
func (c *clientState) waitReverseTCP(timeout time.Duration) *net.TCPConn {
    c.mu.Lock()

    // If already connected, return immediately
    if c.reverseTCP != nil {
        conn := c.reverseTCP
        c.mu.Unlock()
        return conn
    }

    // Create ready channel
    ready := make(chan struct{})
    c.reverseConnReady = ready
    c.mu.Unlock()

    // Wait for connection or timeout
    select {
    case <-ready:
        c.mu.Lock()
        conn := c.reverseTCP
        c.mu.Unlock()
        return conn
    case <-time.After(timeout):
        return nil
    }
}
```

**Apply same changes to**:
- `internal/server/server.go` lines 278-290
- `internal/rpc/session.go` lines 559-571

---

## Additional Improvements (Optional)

### Remove Context Nil-Checks

**Files**: [internal/engine/runner.go](internal/engine/runner.go), [probe/rtt.go](probe/rtt.go), [probe/sampler.go](probe/sampler.go)

Many functions do:
```go
if ctx == nil {
    ctx = context.Background()
}
```

Go convention: callers should always pass non-nil context. Remove these checks and document the requirement in function godoc.

### Add Custom Error Types

**Create**: `internal/errors/errors.go`

```go
package errors

import "errors"

var (
    ErrTimeout        = errors.New("operation timed out")
    ErrSampleMismatch = errors.New("sample ID mismatch")
    ErrInvalidNetwork = errors.New("invalid network type")
    ErrSessionNotFound = errors.New("session not found")
)
```

Replace error strings throughout codebase with these named errors. Enables better error handling with `errors.Is()`.

---

## Quick Checklist

**Critical (must fix before production)**:
- [ ] Fix #1: Add `sync.Once` to RTTSampler.Stop()
- [ ] Fix #2: Fix clientKey() to include port
- [ ] Fix #3: Add RPC call timeouts
- [ ] Fix #4: Fix reverse TCP infinite retry
- [ ] Fix #5: Add UDP ping cleanup

**Quality (should fix soon)**:
- [ ] Fix #6: Validate network in Sampler
- [ ] Fix #7: Remove duplicate progress code
- [ ] Fix #8: Make inter-sample wait cancellable
- [ ] Fix #9: Extract magic numbers to constants
- [ ] Fix #10: Replace busy-wait loops with channels

---

## File Reference

**Files needing changes**:
- [internal/metrics/rtt_sampler.go](internal/metrics/rtt_sampler.go) - Fix #1
- [internal/server/server.go](internal/server/server.go) - Fix #2, #5, #9, #10
- [internal/rpc/client.go](internal/rpc/client.go) - Fix #3
- [internal/engine/samples.go](internal/engine/samples.go) - Fix #4, #8
- [probe/sampler.go](probe/sampler.go) - Fix #6
- [internal/engine/progress.go](internal/engine/progress.go) - Fix #7
- [internal/rpc/session.go](internal/rpc/session.go) - Fix #5, #9, #10

---

**End of Document**
