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
