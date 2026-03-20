package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var uiFS embed.FS

func WebUIHandler(enabled bool) http.Handler {
	if !enabled {
		return http.NotFoundHandler()
	}
	sub, err := fs.Sub(uiFS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.html":
			r.URL.Path = "/"
		case "/auth":
			r.URL.Path = "/auth.html"
		}
		fileServer.ServeHTTP(w, r)
	}))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self' ws: wss:; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
