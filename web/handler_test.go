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
	for _, id := range []string{"identity-summary", "status-summary", "flow-rows", "audit-form", "audit-query", "audit-table-toggle", "audit-raw", "firewall-status"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Fatalf("HTML is missing UI region %q", id)
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
	for _, required := range []string{"requestJSON('/identity')", "state.flows", "renderFlowTable()", "page: 'audit'", "history.replaceState", "refreshPending", "auditPending", "auditGeneration", "performance.now()", "requestPage", "QueryAudit", "audit-query", "audit-table-toggle", "key === 'Escape'", "GetRouteStatus", "SetRouteOverride", "ClearRouteOverride"} {
		if !strings.Contains(script, required) {
			t.Fatalf("app.js is missing behavior %q", required)
		}
	}
	if strings.Contains(script, "flow-filter').addEventListener('input', () => { if (state.page === 'flows') refreshPage()") {
		t.Fatalf("flow filtering still performs a network refresh")
	}
}
