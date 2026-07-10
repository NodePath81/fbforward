package flowcontext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/util"
)

const (
	defaultUnixSocketPath = "/run/fbforward/flow-context.sock"
	maxUnixRequestBytes   = int64(64 << 10)
)

type UnixOptions struct {
	SocketPath        string
	AuthToken         string
	AllowedNamespaces []string
	MaxTTL            time.Duration
	ResolveTimeout    time.Duration
	MaxBodyBytes      int64
	MaxKeyLength      int
	MaxValueLength    int
	SocketMode        fs.FileMode
	RateLimitBurst    int
	RateLimitWindow   time.Duration
	TagPolicy         TagPolicy
}

func DefaultUnixOptions() UnixOptions {
	return UnixOptions{
		SocketPath:      defaultUnixSocketPath,
		MaxTTL:          24 * time.Hour,
		ResolveTimeout:  5 * time.Second,
		MaxBodyBytes:    maxUnixRequestBytes,
		MaxKeyLength:    64,
		MaxValueLength:  256,
		SocketMode:      0660,
		RateLimitBurst:  60,
		RateLimitWindow: time.Minute,
	}
}

func (o UnixOptions) normalized() UnixOptions {
	d := DefaultUnixOptions()
	if strings.TrimSpace(o.SocketPath) != "" {
		d.SocketPath = strings.TrimSpace(o.SocketPath)
	}
	if strings.TrimSpace(o.AuthToken) != "" {
		d.AuthToken = strings.TrimSpace(o.AuthToken)
	}
	policy := o.TagPolicy
	if o.MaxTTL > 0 {
		d.MaxTTL = o.MaxTTL
	} else if policy.MaxTTL > 0 {
		d.MaxTTL = policy.MaxTTL
	}
	if o.ResolveTimeout > 0 {
		d.ResolveTimeout = o.ResolveTimeout
	}
	if o.MaxBodyBytes > 0 && o.MaxBodyBytes <= maxUnixRequestBytes {
		d.MaxBodyBytes = o.MaxBodyBytes
	}
	if o.MaxKeyLength > 0 {
		d.MaxKeyLength = o.MaxKeyLength
	} else if policy.MaxKeyLength > 0 {
		d.MaxKeyLength = policy.MaxKeyLength
	}
	if o.MaxValueLength > 0 {
		d.MaxValueLength = o.MaxValueLength
	} else if policy.MaxValueLength > 0 {
		d.MaxValueLength = policy.MaxValueLength
	}
	if o.SocketMode != 0 {
		d.SocketMode = o.SocketMode
	}
	if o.RateLimitBurst > 0 {
		d.RateLimitBurst = o.RateLimitBurst
	}
	if o.RateLimitWindow > 0 {
		d.RateLimitWindow = o.RateLimitWindow
	}
	if len(o.AllowedNamespaces) > 0 {
		d.AllowedNamespaces = append([]string(nil), o.AllowedNamespaces...)
	} else if len(policy.AllowedNamespaces) > 0 {
		d.AllowedNamespaces = append([]string(nil), policy.AllowedNamespaces...)
	}
	d.TagPolicy = policy
	d.TagPolicy.AllowedNamespaces = append([]string(nil), d.AllowedNamespaces...)
	d.TagPolicy.MaxTTL = d.MaxTTL
	d.TagPolicy.MaxKeyLength = d.MaxKeyLength
	d.TagPolicy.MaxValueLength = d.MaxValueLength
	return d
}

type UnixService struct {
	registry   *Registry
	store      *audit.Store
	options    UnixOptions
	projection *tagProjection
	limiter    *tagRateLimiter
	logger     util.Logger

	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
	closed   bool
}

func NewUnixService(registry *Registry, store *audit.Store, options UnixOptions, logger util.Logger) *UnixService {
	options = options.normalized()
	return &UnixService{
		registry:   registry,
		store:      store,
		options:    options,
		projection: newTagProjection(),
		limiter:    newTagRateLimiter(options.RateLimitBurst, options.RateLimitWindow),
		logger:     util.ComponentLogger(logger, util.CompControl),
	}
}

func (s *UnixService) Start(ctx context.Context) error {
	if s == nil || s.registry == nil || s.store == nil {
		return ErrTagStore
	}
	if s.options.AuthToken == "" {
		return ErrUnauthorized
	}
	s.mu.Lock()
	if s.listener != nil {
		s.mu.Unlock()
		return errors.New("flow context unix service already started")
	}
	path := s.options.SocketPath
	if err := prepareUnixSocket(path); err != nil {
		s.mu.Unlock()
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if err := os.Chmod(path, s.options.SocketMode); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		s.mu.Unlock()
		return err
	}
	s.listener = listener
	s.server = &http.Server{Handler: http.HandlerFunc(s.handleRPC)}
	server := s.server
	s.mu.Unlock()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && s.logger != nil {
			s.logger.Error("flow context unix service stopped", "error", err)
		}
	}()
	if ctx != nil {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.Shutdown(shutdownCtx)
			cancel()
		}()
	}
	return nil
}

func (s *UnixService) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	server := s.server
	listener := s.listener
	path := s.options.SocketPath
	if server == nil && listener == nil {
		s.closed = true
		s.mu.Unlock()
		return nil
	}
	s.server = nil
	s.listener = nil
	s.closed = true
	s.mu.Unlock()
	var err error
	if server != nil {
		err = server.Shutdown(ctx)
	} else if listener != nil {
		err = listener.Close()
	}
	_ = os.Remove(path)
	return err
}

func prepareUnixSocket(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("flow context socket path is empty")
	}
	parent := filepath.Dir(path)
	if parent != "." {
		if err := os.MkdirAll(parent, 0750); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("flow context socket path is not a unix socket")
	}
	if connection, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond); dialErr == nil {
		_ = connection.Close()
		return errors.New("flow context socket is already in use")
	}
	return os.Remove(path)
}

type unixRPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type unixRPCResponse struct {
	Ok     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *UnixService) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/rpc" && r.URL.Path != "/" {
		writeUnixError(w, http.StatusNotFound, "not found")
		return
	}
	if !tokenMatches(r, s.options.AuthToken) {
		writeUnixError(w, http.StatusUnauthorized, ErrUnauthorized.Error())
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeUnixError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.ContentLength > s.options.MaxBodyBytes {
		writeUnixError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.options.MaxBodyBytes)
	var request unixRPCRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeUnixError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeUnixError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeUnixError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !s.limiter.Allow(r.Header.Get("Authorization")) {
		writeUnixError(w, http.StatusTooManyRequests, ErrRateLimited.Error())
		return
	}
	result, err := s.dispatch(r.Context(), strings.TrimSpace(request.Method), request.Params)
	if err != nil {
		writeUnixError(w, unixErrorStatus(err), err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(unixRPCResponse{Ok: true, Result: result})
}

func (s *UnixService) dispatch(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "ResolveFlow":
		var request ResolveFlowRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.ResolveFlow(ctx, request)
		if err != nil {
			return nil, err
		}
		return map[string]any{"flow": newFlowResponse(result)}, nil
	case "SetFlowTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.SetFlowTag(ctx, request)
		return map[string]any{"tag": result}, err
	case "UnsetFlowTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		err := s.UnsetFlowTag(ctx, request)
		return map[string]any{"removed": err == nil}, err
	case "SetClientTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.SetClientTag(ctx, request)
		return map[string]any{"tag": result}, err
	case "UnsetClientTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		err := s.UnsetClientTag(ctx, request)
		return map[string]any{"removed": err == nil}, err
	case "ListFlowTags":
		var request ListFlowTagsRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.ListFlowTags(ctx, request)
		return map[string]any{"tags": result}, err
	default:
		return nil, errors.New("unknown flow context method")
	}
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return ErrInvalidParams
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return ErrInvalidParams
	}
	return nil
}

func unixErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrInvalidTag), errors.Is(err, ErrInvalidTuple):
		return http.StatusBadRequest
	case errors.Is(err, ErrInvalidParams):
		return http.StatusBadRequest
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrFlowNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrClosed), errors.Is(err, ErrTagStore):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeUnixError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(unixRPCResponse{Ok: false, Error: message})
}

type tagRateLimiter struct {
	mu      sync.Mutex
	entries map[string]tagRateEntry
	burst   int
	window  time.Duration
}

type tagRateEntry struct {
	started time.Time
	count   int
}

func newTagRateLimiter(burst int, window time.Duration) *tagRateLimiter {
	if burst <= 0 {
		burst = 60
	}
	if window <= 0 {
		window = time.Minute
	}
	return &tagRateLimiter{entries: make(map[string]tagRateEntry), burst: burst, window: window}
}

func (l *tagRateLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	if key == "" {
		key = "anonymous"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
		l.entries[key] = tagRateEntry{started: now, count: 1}
		return true
	}
	if entry.count >= l.burst {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}
