package util

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/NodePath81/fbforward/internal/version"
)

type Logger = *slog.Logger

const (
	CompLifecycle  = "lifecycle"
	CompForwardTCP = "forward.tcp"
	CompForwardUDP = "forward.udp"
	CompMeasure    = "measure"
	CompProbe      = "probe"
	CompCoord      = "coordination"
	CompControl    = "control"
	CompDNS        = "dns"
	CompShaping    = "shaping"
	CompUpstream   = "upstream"
	CompGeoIP      = "geoip"
	CompIPLog      = "iplog"
	CompFirewall   = "firewall"
	CompNotify     = "notify"
)

func NewLogger(level, format string) *slog.Logger {
	lvl := parseLevel(level)
	lv := &slog.LevelVar{}
	lv.Set(lvl)

	if strings.EqualFold(strings.TrimSpace(format), "json") {
		return slog.New(newOTelHandler(lv))
	}

	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: lv,
	}))
}

func ComponentLogger(parent Logger, component string) Logger {
	if parent == nil {
		return nil
	}
	return parent.With("component", component)
}

func Event(logger Logger, level slog.Level, eventName string, attrs ...any) {
	if logger == nil {
		return
	}
	args := make([]any, 0, len(attrs)+2)
	args = append(args, "event.name", eventName)
	args = append(args, attrs...)
	logger.Log(context.Background(), level, eventName, args...)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type otelHandlerState struct {
	level *slog.LevelVar
	pool  sync.Pool
	mu    sync.Mutex
}

type otelHandler struct {
	state  *otelHandlerState
	attrs  []slog.Attr
	groups []string
}

func newOTelHandler(level *slog.LevelVar) *otelHandler {
	state := &otelHandlerState{level: level}
	state.pool.New = func() any {
		return &bytes.Buffer{}
	}
	return &otelHandler{state: state}
}

func (h *otelHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h == nil || h.state == nil || h.state.level == nil {
		return false
	}
	return level >= h.state.level.Level()
}

func (h *otelHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any, len(h.attrs)+8)
	for _, attr := range h.attrs {
		appendAttr(attrs, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttr(attrs, h.groups, attr)
		return true
	})

	severity, severityNumber := mapSeverity(record.Level)
	payload := map[string]any{
		"ts":              record.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		"severity":        severity,
		"severity_number": severityNumber,
		"body":            record.Message,
		"attributes":      attrs,
		"resource": map[string]any{
			"service.name":    "fbforward",
			"service.version": version.Version,
		},
	}

	buf := h.state.pool.Get().(*bytes.Buffer)
	buf.Reset()
	defer h.state.pool.Put(buf)
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return err
	}

	h.state.mu.Lock()
	_, err := os.Stdout.Write(buf.Bytes())
	h.state.mu.Unlock()
	return err
}

func (h *otelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &otelHandler{
		state:  h.state,
		attrs:  make([]slog.Attr, 0, len(h.attrs)+len(attrs)),
		groups: append([]string(nil), h.groups...),
	}
	next.attrs = append(next.attrs, h.attrs...)
	next.attrs = append(next.attrs, attrs...)
	return next
}

func (h *otelHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := &otelHandler{
		state:  h.state,
		attrs:  append([]slog.Attr(nil), h.attrs...),
		groups: make([]string, 0, len(h.groups)+1),
	}
	next.groups = append(next.groups, h.groups...)
	next.groups = append(next.groups, name)
	return next
}

func appendAttr(dst map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Key == "" {
		if attr.Value.Kind() != slog.KindGroup {
			return
		}
		for _, nested := range attr.Value.Group() {
			appendAttr(dst, groups, nested)
		}
		return
	}
	key := attr.Key
	if len(groups) > 0 {
		parts := append([]string{}, groups...)
		parts = append(parts, key)
		key = strings.Join(parts, ".")
	}
	if attr.Value.Kind() == slog.KindGroup {
		nextGroups := append([]string{}, groups...)
		nextGroups = append(nextGroups, attr.Key)
		for _, nested := range attr.Value.Group() {
			appendAttr(dst, nextGroups, nested)
		}
		return
	}
	dst[key] = valueToAny(attr.Value)
}

func valueToAny(value slog.Value) any {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	case slog.KindAny:
		return value.Any()
	case slog.KindGroup:
		group := make(map[string]any)
		for _, nested := range value.Group() {
			appendAttr(group, nil, nested)
		}
		return group
	default:
		return value.Any()
	}
}

func mapSeverity(level slog.Level) (string, int) {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG", 5
	case level < slog.LevelWarn:
		return "INFO", 9
	case level < slog.LevelError:
		return "WARN", 13
	default:
		return "ERROR", 17
	}
}
