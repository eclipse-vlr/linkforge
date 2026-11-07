// Package httpapi provides the LinkForge HTTP interface.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

type linkHandlers struct {
	store            link.Repository
	logger           *slog.Logger
	baseURL          string
	createLinkAPIKey string
}

func NewRouter(store link.Repository, logger *slog.Logger, baseURL, createLinkAPIKey string) http.Handler {
	handlers := linkHandlers{store: store, logger: logger, baseURL: baseURL, createLinkAPIKey: createLinkAPIKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.Handle("/api/v1/links", newCreateLinkLimiter().middleware(http.HandlerFunc(handlers.createLink)))
	mux.HandleFunc("/api/v1/links/{code}/stats", handlers.linkStats)
	mux.HandleFunc("/", handlers.redirect)
	return mux
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}
