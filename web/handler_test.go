package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesStaticAssetsWithSecurityHeaders(t *testing.T) {
	server := Handler()
	for path, contentType := range map[string]string{"/": "text/html", "/app.css": "text/css", "/app.js": "text/javascript"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), contentType) {
			t.Fatalf("%s: status=%d content-type=%q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: missing cache/frame headers", path)
		}
	}
}

func TestHandlerRejectsUnknownAndUnsafeAssets(t *testing.T) {
	server := Handler()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected unknown asset 404, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/app.js", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected POST 405, got %d", rec.Code)
	}
}

func TestHTMLHasNoInlineOrExternalResources(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if strings.Contains(body, "<style") || strings.Contains(body, "<script>") || strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Fatalf("HTML contains inline or external resources")
	}
	if !strings.Contains(body, `script type="module" src="/app.js"`) || !strings.Contains(body, `href="/app.css"`) {
		t.Fatalf("HTML does not use same-origin external assets")
	}
	for _, id := range []string{"login-error", "instance-summary", "upstream-rows", "route-rows", "flow-rows", "audit-form", "audit-query", "audit-view-toggle", "audit-raw", "firewall-status"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Fatalf("HTML is missing UI region %q", id)
		}
	}
	for _, removed := range []string{"Local forwarding control", "Text-first operator interface", "Store in this session", "not loaded", "<footer"} {
		if strings.Contains(body, removed) {
			t.Fatalf("HTML still contains nonessential copy %q", removed)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("unexpected CSP: %q", csp)
	}
}

func TestClientSecurityInvariants(t *testing.T) {
	data, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	script := string(data)
	for _, forbidden := range []string{"innerHTML", "localStorage", "document.cookie"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("app.js contains forbidden client state or HTML sink %q", forbidden)
		}
	}
	for _, forbidden := range []string{"searchParams.set('token'", "searchParams.append('token'", "?token=", "&token="} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("app.js places the control token in the URL: %q", forbidden)
		}
	}
	if !strings.Contains(script, "sessionStorage") || !strings.Contains(script, "Authorization") {
		t.Fatalf("app.js does not use session-scoped bearer authentication")
	}
	for _, required := range []string{"requestJSON('/identity')", "logout('invalid token')", "showLoginError('')", "if (state.identityLoaded) return", "state.flows", "renderFlowTable()", "page: 'audit'", "history.replaceState", "refreshPending", "auditPending", "auditGeneration", "performance.now()", "requestPage", "QueryAudit", "audit-query", "audit-view-toggle", "key === 'Escape'", "pollingIntervals = { status: 5000, flows: 2000 }", "setAttribute('aria-current', 'page')", "SetRouteOverride", "ClearRouteOverride"} {
		if !strings.Contains(script, required) {
			t.Fatalf("app.js is missing behavior %q", required)
		}
	}
	for _, redundant := range []string{"GetRouteStatus", "GetScheduleStatus", "GetIPLogStatus"} {
		if strings.Contains(script, redundant) {
			t.Fatalf("app.js still requests redundant status RPC %q", redundant)
		}
	}
	if strings.Count(script, "rpc('GetStatus')") != 1 {
		t.Fatalf("status refresh must issue exactly one status RPC")
	}
	if strings.Contains(script, "flow-filter').addEventListener('input', () => { if (state.page === 'flows') refreshPage()") {
		t.Fatalf("flow filtering still performs a network refresh")
	}
	css, err := assets.ReadFile("app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	stylesheet := string(css)
	for _, required := range []string{`nav button[aria-current="page"]`, ".sr-only", "width: max-content", "min-width: 100%"} {
		if !strings.Contains(stylesheet, required) {
			t.Fatalf("app.css is missing responsive behavior %q", required)
		}
	}
	if strings.Contains(stylesheet, "min-width: 42rem") {
		t.Fatalf("app.css still forces wide tables")
	}
}
