package web

import (
	"net/http"
	"strings"
	"time"
)

func Harden(next http.Handler) http.Handler {
	requests := make(chan struct{}, 128)
	streams := make(chan struct{}, 64)
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stream := strings.HasSuffix(r.URL.Path, "/events") || strings.HasPrefix(r.URL.Path, "/mcp")
		gate := requests
		if stream {
			gate = streams
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			http.Error(w, "too many concurrent requests", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		controller := http.NewResponseController(w)
		if !stream {
			_ = controller.SetReadDeadline(time.Now().Add(30 * time.Second))
			_ = controller.SetWriteDeadline(time.Now().Add(2 * time.Minute))
		}
		next.ServeHTTP(w, r)
	}))
}
