package probe

import (
	"context"
	"math/rand"
	"net"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type probeWindow struct {
	size    int
	samples int
	lost    int
	rttsMs  []float64
}

func newProbeWindow(size int) *probeWindow {
	return &probeWindow{size: size}
}

func (w *probeWindow) addSample(ok bool, rtt time.Duration) {
	w.samples++
	if !ok {
		w.lost++
		return
	}
	w.rttsMs = append(w.rttsMs, float64(rtt.Microseconds())/1000.0)
}

func (w *probeWindow) complete() bool {
	return w.samples >= w.size
}

func (w *probeWindow) reset() {
	w.samples = 0
	w.lost = 0
	w.rttsMs = w.rttsMs[:0]
}

func (w *probeWindow) metrics() upstream.WindowMetrics {
	loss := float64(w.lost) / float64(w.size)
	var avg float64
	if len(w.rttsMs) > 0 {
		var sum float64
		for _, v := range w.rttsMs {
			sum += v
		}
		avg = sum / float64(len(w.rttsMs))
	}
	jitter := computeJitter(w.rttsMs)
	return upstream.WindowMetrics{
		Loss:     clampFloat(loss, 0, 1),
		AvgRTTMs: avg,
		JitterMs: jitter,
		HasRTT:   len(w.rttsMs) > 0,
	}
}

func computeJitter(samples []float64) float64 {
	// Jitter is mean absolute difference between consecutive RTT samples.
	if len(samples) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(samples); i++ {
		diff := samples[i] - samples[i-1]
		if diff < 0 {
			diff = -diff
		}
		sum += diff
	}
	return sum / float64(len(samples)-1)
}

func ProbeLoop(ctx context.Context, up *upstream.Upstream, cfg config.ProbeConfig, manager *upstream.UpstreamManager, metrics *metrics.Metrics, logger util.Logger) {
	timeout := cfg.Interval.Duration()
	if timeout <= 0 {
		timeout = time.Second
	}
	retryDelay := timeout
	if retryDelay <= 0 {
		retryDelay = time.Second
	}
	id := rand.Intn(0xffff)
	var seq uint16

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ip := up.ActiveIP()
		if ip == nil {
			logger.Error("probe waiting, no resolved IP", "upstream", up.Tag)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
				continue
			}
		}

		isV4 := ip.To4() != nil
		network := "ip4:icmp"
		proto := 1
		echoType := icmp.Type(ipv4.ICMPTypeEcho)
		echoReplyType := icmp.Type(ipv4.ICMPTypeEchoReply)
		if !isV4 {
			network = "ip6:ipv6-icmp"
			proto = 58
			echoType = icmp.Type(ipv6.ICMPTypeEchoRequest)
			echoReplyType = icmp.Type(ipv6.ICMPTypeEchoReply)
		}

		conn, err := icmp.ListenPacket(network, "")
		if err != nil {
			logger.Error("probe socket error", "upstream", up.Tag, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
				continue
			}
		}

		window := newProbeWindow(cfg.WindowSize)
		ticker := time.NewTicker(cfg.Interval.Duration())
	probeLoop:
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				_ = conn.Close()
				return
			case <-ticker.C:
				current := up.ActiveIP()
				if current == nil || !current.Equal(ip) {
					ticker.Stop()
					_ = conn.Close()
					break probeLoop
				}
				seq++
				rtt, ok := sendPing(conn, ip, id, seq, echoType, echoReplyType, proto, timeout)
				window.addSample(ok, rtt)
				if window.complete() {
					wm := window.metrics()
					stats := manager.UpdateReachability(up.Tag, wm.Loss < 1)
					if metrics != nil {
						metrics.SetUpstreamMetrics(up.Tag, stats)
					}
					window.reset()
				}
			}
		}
	}
}

func sendPing(conn *icmp.PacketConn, ip net.IP, id int, seq uint16, echoType, replyType icmp.Type, proto int, timeout time.Duration) (time.Duration, bool) {
	msg := icmp.Message{
		Type: echoType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  int(seq),
			Data: []byte("fbforward"),
		},
	}
	payload, err := msg.Marshal(nil)
	if err != nil {
		return 0, false
	}
	dst := &net.IPAddr{IP: ip}
	start := time.Now()
	if _, err := conn.WriteTo(payload, dst); err != nil {
		return 0, false
	}

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, false
	}
	buf := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, false
		}
		ipAddr, ok := peer.(*net.IPAddr)
		if ok && ipAddr.IP != nil && !ipAddr.IP.Equal(ip) {
			continue
		}
		parsed, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		if parsed.Type != replyType {
			continue
		}
		echo, ok := parsed.Body.(*icmp.Echo)
		if !ok {
			continue
		}
		if echo.ID == id && echo.Seq == int(seq) {
			return time.Since(start), true
		}
	}
}

func clampFloat(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}
