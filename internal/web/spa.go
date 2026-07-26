package web

import (
	"io/fs"
	"net/http"
	"strings"
)

// SPAHandler serves embedded static assets from distFS and falls back to
// index.html for unknown non-API paths (single-page-app routing).
func SPAHandler(distFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(distFS))
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/runs/") {
			http.NotFound(w, r)
			return
		}
		assetPath := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/"), "./")
		if assetPath == "" {
			assetPath = "index.html"
		}
		if _, err := fs.Stat(distFS, assetPath); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeFileFS(w, r, distFS, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}