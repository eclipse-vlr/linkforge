package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

func (h linkHandlers) linkStats(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	code := r.PathValue("code")
	if !link.ValidCode(code) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	stats, err := h.store.GetStats(ctx, code)
	if err != nil {
		if errors.Is(err, link.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "failed to get link stats", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		h.logger.ErrorContext(r.Context(), "failed to write stats response", "error", err)
	}
}

func (h linkHandlers) redirect(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	code := strings.TrimPrefix(r.URL.Path, "/")

	if !link.ValidCode(code) {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	originalURL, err := h.store.Get(ctx, code)
	if err != nil {
		if errors.Is(err, link.ErrNotFound) {
			http.NotFound(w, r)
			return
		}

		h.logger.ErrorContext(r.Context(), "failed to get link", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	statsCtx, cancelStats := context.WithTimeout(r.Context(), 500*time.Millisecond)
	err = h.store.IncrementClicks(statsCtx, code)
	cancelStats()
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to increment link clicks", "code", code, "error", err)
	}

	http.Redirect(w, r, originalURL, http.StatusFound)
}
