package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	newRouter(NewLinkStore(), newTestLogger(), "https://sho.rt").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("expected application/json, got %q", got)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %q", body["status"])
	}
}

func TestHealthzMethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	newRouter(NewLinkStore(), newTestLogger(), "https://sho.rt").ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestUnknownRoute(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rec := httptest.NewRecorder()

	newRouter(NewLinkStore(), newTestLogger(), "https://sho.rt").ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestCreateLink(t *testing.T) {
	store := NewLinkStore()
	handler := newRouter(store, newTestLogger(), "https://sho.rt")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/links",
		strings.NewReader(`{"url":"https://example.com/article"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var response CreateLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if len(response.Code) != 8 {
		t.Errorf("expected code length 8, got %d", len(response.Code))
	}

	if response.OriginalURL != "https://example.com/article" {
		t.Errorf("unexpected original URL: %q", response.OriginalURL)
	}

	if response.ShortURL != "https://sho.rt/"+response.Code {
		t.Errorf("unexpected short URL: %q", response.ShortURL)
	}

	savedURL, err := store.Get(context.Background(), response.Code)
	if err != nil {
		t.Fatalf("created link was not saved: %v", err)
	}

	if savedURL != response.OriginalURL {
		t.Errorf(
			"stored URL %q does not match response URL %q",
			savedURL,
			response.OriginalURL,
		)
	}
}

func TestCreateLinkRejectsEmptyURL(t *testing.T) {
	handler := newRouter(NewLinkStore(), newTestLogger(), "https://sho.rt")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/links",
		strings.NewReader(`{"url":"   "}`),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestRedirectToOriginalURL(t *testing.T) {
	store := NewLinkStore()

	if err := store.Save(
		context.Background(),
		"abc12345",
		"https://example.com/article",
	); err != nil {
		t.Fatalf("failed to save test link: %v", err)
	}

	handler := newRouter(store, newTestLogger(), "https://sho.rt")

	req := httptest.NewRequest(http.MethodGet, "/abc12345", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected status 302, got %d", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != "https://example.com/article" {
		t.Errorf("unexpected Location header: %q", got)
	}
}

func TestRedirectUnknownCode(t *testing.T) {
	handler := newRouter(NewLinkStore(), newTestLogger(), "https://sho.rt")

	req := httptest.NewRequest(http.MethodGet, "/unknown1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid HTTPS URL",
			url:     "https://example.com/article",
			wantErr: false,
		},
		{
			name:    "valid HTTP URL",
			url:     "http://localhost:8080/page",
			wantErr: false,
		},
		{
			name:    "missing scheme",
			url:     "example.com",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			url:     "ftp://example.com",
			wantErr: true,
		},
		{
			name:    "missing hostname",
			url:     "https:///article",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url)

			if (err != nil) != tt.wantErr {
				t.Errorf(
					"validateURL(%q) error = %v, wantErr %v",
					tt.url,
					err,
					tt.wantErr,
				)
			}
		})
	}
}

func TestCreateLinkRejectsInvalidURL(t *testing.T) {
	store := NewLinkStore()
	handler := newRouter(store, newTestLogger(), "https://sho.rt")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/links",
		strings.NewReader(`{"url":"ftp://example.com"}`),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	if len(store.links) != 0 {
		t.Errorf("expected no links to be saved, got %d", len(store.links))
	}
}

type conflictLinkRepository struct {
	saveCalls int
}

func (s *conflictLinkRepository) GetStats(ctx context.Context, code string) (LinkStats, error) {
	return LinkStats{}, ErrLinkNotFound
}

func (s *conflictLinkRepository) IncrementClicks(ctx context.Context, code string) error {
	return ErrLinkNotFound
}

func (s *conflictLinkRepository) Save(
	ctx context.Context,
	code string,
	originalURL string,
) error {
	s.saveCalls++
	return ErrCodeConflict
}

func (s *conflictLinkRepository) Get(
	ctx context.Context,
	code string,
) (string, error) {
	return "", ErrLinkNotFound
}

func TestCreateLinkReturnsErrorAfterMaxConflicts(t *testing.T) {
	store := &conflictLinkRepository{}
	handler := newRouter(store, newTestLogger(), "https://sho.rt")

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/links",
		strings.NewReader(`{"url":"https://example.com/article"}`),
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf(
			"expected status 500, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	if store.saveCalls != 5 {
		t.Errorf("expected 5 save attempts, got %d", store.saveCalls)
	}
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
