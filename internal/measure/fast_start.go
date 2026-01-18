package measure

import (
	"context"
	"net"
	"strconv"
	"time"
)

func FastStartProbe(ctx context.Context, host string, port int, timeout time.Duration) (float64, bool) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, false
	}
	_ = conn.Close()
	return float64(time.Since(start)) / float64(time.Millisecond), true
}

func FastStartScore(rttMs float64, reachable bool, priority float64) float64 {
	if !reachable {
		return 0
	}
	ref := 50.0
	return 100/(1+rttMs/ref) + priority
}
