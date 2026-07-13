package control

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/upstream"
)

func TestRouteOverrideRPCAndStatus(t *testing.T) {
	a := &upstream.Upstream{Tag: "a"}
	b := &upstream.Upstream{Tag: "b"}
	a.SetActiveIP(net.ParseIP("192.0.2.1"))
	b.SetActiveIP(net.ParseIP("192.0.2.2"))
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{a, b}, nil)
	selector := upstream.NewRouteSelector(manager, []config.RouteConfig{{Name: "web", Strategy: "static", Upstreams: []string{"a", "b"}, DefaultUpstream: "a"}})
	server := newTestControlServer(t)
	server.SetRouteStateReader(routeReaderAdapter{selector})

	call := func(method string, params any) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, method, params)))
		req.Header.Set("Authorization", "Bearer 0123456789abcdef")
		rec := httptest.NewRecorder()
		server.handleRPC(rec, req)
		return rec
	}

	status := call("GetRouteStatus", nil)
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"default_upstream":"a"`)) {
		t.Fatalf("unexpected route status: %d %s", status.Code, status.Body.String())
	}
	set := call("SetRouteOverride", map[string]any{"route": "web", "upstream": "b"})
	if set.Code != http.StatusOK {
		t.Fatalf("set override failed: %d %s", set.Code, set.Body.String())
	}
	selected, _, err := selector.Pick("web")
	if err != nil || selected.Tag != "b" {
		t.Fatalf("override not applied: %v %v", selected, err)
	}
	clear := call("ClearRouteOverride", map[string]any{"route": "web"})
	if clear.Code != http.StatusOK {
		t.Fatalf("clear override failed: %d %s", clear.Code, clear.Body.String())
	}
	selected, _, err = selector.Pick("web")
	if err != nil || selected.Tag != "a" {
		t.Fatalf("default not restored: %v %v", selected, err)
	}
}

func TestLegacySetUpstreamRejectsAmbiguousMultipleRoutes(t *testing.T) {
	manager := upstream.NewUpstreamManager(nil, nil)
	selector := upstream.NewRouteSelector(manager, []config.RouteConfig{
		{Name: "one", Strategy: "static", Upstreams: []string{"a"}, DefaultUpstream: "a"},
		{Name: "two", Strategy: "static", Upstreams: []string{"a"}, DefaultUpstream: "a"},
	})
	server := newTestControlServer(t)
	server.SetRouteStateReader(routeReaderAdapter{selector})
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "SetUpstream", map[string]any{"mode": "auto"})))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected ambiguous legacy mode to fail, got %d %s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Error == "" {
		t.Fatalf("unexpected error response: %s", rec.Body.String())
	}
}

func TestLegacySetUpstreamMapsToSingleRouteOverride(t *testing.T) {
	a := &upstream.Upstream{Tag: "a"}
	b := &upstream.Upstream{Tag: "b"}
	a.SetActiveIP(net.ParseIP("192.0.2.1"))
	b.SetActiveIP(net.ParseIP("192.0.2.2"))
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{a, b}, nil)
	selector := upstream.NewRouteSelector(manager, []config.RouteConfig{{Name: "web", Strategy: "static", Upstreams: []string{"a", "b"}, DefaultUpstream: "a"}})
	server := newTestControlServer(t)
	server.SetRouteStateReader(routeReaderAdapter{selector})

	call := func(params map[string]any) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "SetUpstream", params)))
		req.Header.Set("Authorization", "Bearer 0123456789abcdef")
		rec := httptest.NewRecorder()
		server.handleRPC(rec, req)
		return rec
	}
	if rec := call(map[string]any{"mode": "manual", "tag": "b"}); rec.Code != http.StatusOK {
		t.Fatalf("legacy manual compatibility failed: %d %s", rec.Code, rec.Body.String())
	}
	selected, _, err := selector.Pick("web")
	if err != nil || selected.Tag != "b" {
		t.Fatalf("legacy manual did not set route override: %v %v", selected, err)
	}
	if rec := call(map[string]any{"mode": "auto"}); rec.Code != http.StatusOK {
		t.Fatalf("legacy auto compatibility failed: %d %s", rec.Code, rec.Body.String())
	}
	selected, _, err = selector.Pick("web")
	if err != nil || selected.Tag != "a" {
		t.Fatalf("legacy auto did not clear route override: %v %v", selected, err)
	}
}

type routeReaderAdapter struct{ selector *upstream.RouteSelector }

func (r routeReaderAdapter) RouteStatus() []upstream.RouteStatus { return r.selector.Status() }
func (r routeReaderAdapter) SetRouteOverride(route, tag string) error {
	return r.selector.SetOverride(route, tag)
}
func (r routeReaderAdapter) ClearRouteOverride(route string) error {
	return r.selector.ClearOverride(route)
}

var _ routeStateReader = routeReaderAdapter{}
