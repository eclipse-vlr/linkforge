// Package httpapi provides the LinkForge HTTP interface.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

const maxCreateLinkBodyBytes = 16 * 1024

type CreateLinkRequest struct {
	URL string `json:"url"`
}

type CreateLinkResponse struct {
	Code        string `json:"code"`
	ShortURL    string `json:"short_url"`
	OriginalURL string `json:"original_url"`
}

func NewRouter(store link.Repository, logger *slog.Logger, baseURL, createLinkAPIKey string) http.Handler {
	mux := http.NewServeMux()
	createLimiter := newCreateLinkLimiter()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	mux.Handle("/api/v1/links", createLimiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// An empty configured key leaves creation available for local development.
		if expectedKey := createLinkAPIKey; expectedKey != "" {
			providedKey := r.Header.Get("X-API-Key")
			if subtle.ConstantTimeCompare([]byte(providedKey), []byte(expectedKey)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxCreateLinkBodyBytes)
		req, err := decodeCreateLinkRequest(r.Body)
		if err != nil {
			writeJSONRequestError(w, err)
			return
		}

		req.URL = strings.TrimSpace(req.URL)

		if req.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}

		if err := link.ValidateURL(req.URL); err != nil {
			http.Error(w, "invalid URL: "+err.Error(), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		var code string
		saved := false

		const maxAttempts = 5

		for attempt := 0; attempt < maxAttempts; attempt++ {
			generatedCode, err := link.GenerateCode()
			if err != nil {
				logger.ErrorContext(r.Context(), "failed to generate code", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			err = store.Save(ctx, generatedCode, req.URL)
			if err == nil {
				code = generatedCode
				saved = true
				break
			}

			if errors.Is(err, link.ErrCodeConflict) {
				continue
			}

			logger.ErrorContext(r.Context(), "failed to save link", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if !saved {
			logger.ErrorContext(r.Context(), "failed to generate unique code", "attempts", maxAttempts)
			http.Error(w, "failed to generate unique code", http.StatusInternalServerError)
			return
		}

		if code == "" {
			http.Error(
				w,
				"failed to generate unique code",
				http.StatusInternalServerError,
			)
			return
		}

		response := CreateLinkResponse{
			Code:        code,
			ShortURL:    baseURL + "/" + code,
			OriginalURL: req.URL,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.ErrorContext(r.Context(), "failed to write response", "error", err)
		}
	})))

	mux.HandleFunc("/api/v1/links/{code}/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		code := r.PathValue("code")
		if !link.ValidCode(code) {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		stats, err := store.GetStats(ctx, code)
		if err != nil {
			if errors.Is(err, link.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			logger.ErrorContext(r.Context(), "failed to get link stats", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(stats); err != nil {
			logger.ErrorContext(r.Context(), "failed to write stats response", "error", err)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		code := strings.TrimPrefix(r.URL.Path, "/")

		if !link.ValidCode(code) {
			http.NotFound(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		originalURL, err := store.Get(ctx, code)
		if err != nil {
			if errors.Is(err, link.ErrNotFound) {
				http.NotFound(w, r)
				return
			}

			logger.ErrorContext(r.Context(), "failed to get link", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		statsCtx, cancelStats := context.WithTimeout(r.Context(), 500*time.Millisecond)
		err = store.IncrementClicks(statsCtx, code)
		cancelStats()
		if err != nil {
			logger.ErrorContext(r.Context(), "failed to increment link clicks", "code", code, "error", err)
		}

		http.Redirect(w, r, originalURL, http.StatusFound)
	})

	return mux
}

func decodeCreateLinkRequest(body io.Reader) (CreateLinkRequest, error) {
	decoder := json.NewDecoder(body)
	invalid := errors.New("expected an object containing exactly one url string")
	opening, err := decoder.Token()
	if err != nil {
		return CreateLinkRequest{}, err
	}
	if opening != json.Delim('{') {
		return CreateLinkRequest{}, invalid
	}
	name, err := decoder.Token()
	if err != nil {
		return CreateLinkRequest{}, err
	}
	if name != "url" {
		return CreateLinkRequest{}, invalid
	}
	var value *string
	if err := decoder.Decode(&value); err != nil {
		return CreateLinkRequest{}, err
	}
	if value == nil {
		return CreateLinkRequest{}, invalid
	}
	// Inspect the closing token: struct decoding accepts duplicate and folded keys.
	closing, err := decoder.Token()
	if err != nil {
		return CreateLinkRequest{}, err
	}
	if closing != json.Delim('}') {
		return CreateLinkRequest{}, invalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			err = invalid
		}
		return CreateLinkRequest{}, err
	}
	return CreateLinkRequest{URL: *value}, nil
}

func writeJSONRequestError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "body must contain exactly one JSON object with a url field", http.StatusBadRequest)
}
