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
