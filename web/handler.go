package web

import (
	"embed"
	"net/http"
)

//go:embed index.html app.css app.js
var assets embed.FS

// Handler serves the small API client. Assets are immutable source files, so
// the handler deliberately disables browser caching between daemon upgrades.
func Handler() http.Handler {
	mux := http.NewServeMux()
	index := asset("index.html", "text/html; charset=utf-8")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		index(w, r)
	})
	mux.HandleFunc("/app.css", asset("app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/app.js", asset("app.js", "text/javascript; charset=utf-8"))
	return securityHeaders(mux)
}

func asset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data, err := assets.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
