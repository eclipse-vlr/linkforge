package main

import (
	"log/slog"
	"net/http"
	"time"
)

// responseWriter records the first final status while forwarding the response.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	// Informational responses do not commit the final response status.
	if w.status == 0 && status >= 200 {
		w.status = status
	}
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	// Let the underlying writer send implicit headers and detect Content-Type.
	return w.ResponseWriter.Write(body)
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		writer := &responseWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)

		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.InfoContext(r.Context(), "HTTP request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", float64(time.Since(start))/float64(time.Millisecond),
		)
	})
}
