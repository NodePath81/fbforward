package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed ui
var uiFS embed.FS

func WebUIHandler(enabled bool) http.Handler {
	if !enabled {
		return http.NotFoundHandler()
	}
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			r.URL.Path = "/"
		case "/index.html":
			r.URL.Path = "/"
		case "/auth":
			r.URL.Path = "/auth.html"
		}
		fileServer.ServeHTTP(w, r)
	})
}
