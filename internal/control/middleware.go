package control

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
)

type requestCtx struct {
	id         string
	start      time.Time
	protocol   string
	method     string
	path       string
	clientAddr string
	clientIP   string
	userAgent  string
	authMethod string
	authOK     bool
}

type statusWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.written {
		sw.code = code
		sw.written = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.written {
		sw.code = http.StatusOK
		sw.written = true
	}
	return sw.ResponseWriter.Write(b)
}

func (c *ControlServer) newRequestCtx(r *http.Request, protocol string, authOK bool) requestCtx {
	id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if id == "" {
		id = fmt.Sprintf("r-%d", atomic.AddUint64(&c.nextReqID, 1))
	}
	return requestCtx{
		id:         id,
		start:      time.Now(),
		protocol:   protocol,
		method:     r.Method,
		path:       r.URL.Path,
		clientAddr: r.RemoteAddr,
		clientIP:   clientIP(r),
		userAgent:  r.UserAgent(),
		authMethod: authMethodForRequest(r),
		authOK:     authOK,
	}
}

func (c *ControlServer) httpMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Endpoint handlers own authentication and protocol-specific limits so
		// direct handler tests and the public routes have identical semantics.
		if id := strings.TrimSpace(r.Header.Get("X-Request-ID")); id == "" || len(id) > 128 {
			r.Header.Set("X-Request-ID", fmt.Sprintf("r-%d", atomic.AddUint64(&c.nextReqID, 1)))
		}
		w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
		next.ServeHTTP(w, r)
	})
}

func authMethodForRequest(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth != "" {
		if _, ok := bearerToken(r); ok {
			return "bearer"
		}
		return "unknown"
	}
	return "none"
}

func (c *ControlServer) policyAttrs() []any {
	return []any{
		"access.policy.name", "none",
		"access.policy.decision", "not_applicable",
		"access.policy.reason", "no_policy_configured",
	}
}

func requestAttrs(req requestCtx) []any {
	return []any{
		"request.id", req.id,
		"request.protocol", req.protocol,
		"request.method", req.method,
		"request.path", req.path,
		"client.addr", req.clientAddr,
		"client.ip", req.clientIP,
		"http.user_agent", req.userAgent,
		"auth.identity", "unknown",
		"auth.method", req.authMethod,
		"auth.authenticated", req.authOK,
	}
}

func completionResult(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return "success"
	case statusCode == 401 || statusCode == 403 || statusCode == 429:
		return "denied"
	case statusCode == 400 || statusCode == 404 || statusCode == 405:
		return "rejected"
	default:
		return "failed"
	}
}

func (c *ControlServer) auditPolicy(req requestCtx, event string) {
	attrs := append([]any{
		"request.id", req.id,
		"client.ip", req.clientIP,
		"request.method", req.method,
		"request.path", req.path,
		"http.user_agent", req.userAgent,
		"auth.identity", "unknown",
		"auth.method", req.authMethod,
		"auth.authenticated", req.authOK,
	}, c.policyAttrs()...)
	attrs = append(attrs, "result", "success")
	util.Event(c.logger, slogLevelInfo(), event, attrs...)
}

func (c *ControlServer) auditFailure(req requestCtx, event, message string, status int, result string) {
	attrs := append(requestAttrs(req), "error", message, "http.status_code", status, "result", result)
	util.Event(c.logger, slogLevelWarn(), event, attrs...)
}

func (c *ControlServer) auditCompletion(sw *statusWriter, req requestCtx, completionErr *string, event string) {
	code := sw.code
	if !sw.written {
		code = 0
	}
	result := completionResult(code)
	if code == 0 {
		result = "failed"
	}
	attrs := append(requestAttrs(req),
		"http.status_code", code,
		"result", result,
		"latency_ms", time.Since(req.start).Milliseconds(),
	)
	if result != "success" && completionErr != nil && *completionErr != "" {
		attrs = append(attrs, "error", *completionErr)
	}
	util.Event(c.logger, slogLevelInfo(), event, attrs...)
}

// These helpers keep the middleware independent of the logger implementation's
// concrete slog levels at call sites and make the audit flow easy to test.
func slogLevelInfo() slog.Level { return slog.LevelInfo }
func slogLevelWarn() slog.Level { return slog.LevelWarn }

func (c *ControlServer) checkAuth(r *http.Request) bool {
	token, ok := bearerToken(r)
	return ok && secureTokenEqual(token, c.cfg.AuthToken)
}

func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	return token, token != ""
}

func secureTokenEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (c *ControlServer) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, rpcResponse{Ok: false, Error: message})
}

type rateLimiter struct {
	mu      sync.Mutex
	clients map[string]*clientLimiter
	rate    float64
	burst   float64
	ttl     time.Duration
}

type clientLimiter struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate float64, burst int, ttl time.Duration) *rateLimiter {
	return &rateLimiter{clients: make(map[string]*clientLimiter), rate: rate, burst: float64(burst), ttl: ttl}
}

func (r *rateLimiter) Allow(key string) bool {
	if key == "" {
		return false
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	limiter := r.clients[key]
	if limiter != nil && now.Sub(limiter.last) > r.ttl {
		delete(r.clients, key)
		limiter = nil
	}
	if limiter == nil {
		r.clients[key] = &clientLimiter{tokens: r.burst - 1, last: now}
		return true
	}
	elapsed := now.Sub(limiter.last).Seconds()
	limiter.tokens = minFloat(r.burst, limiter.tokens+elapsed*r.rate)
	limiter.last = now
	if limiter.tokens < 1 {
		return false
	}
	limiter.tokens--
	return true
}

func (r *rateLimiter) SweepExpired() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, limiter := range r.clients {
		if limiter == nil || now.Sub(limiter.last) > r.ttl {
			delete(r.clients, key)
		}
	}
}

func (r *rateLimiter) RunCleanup(ctxDone <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = r.ttl
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone:
			return
		case <-ticker.C:
			r.SweepExpired()
		}
	}
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
