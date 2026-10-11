package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodGet,
		"/healthz",
		nil,
	)

	rec := httptest.NewRecorder()

	newRouter(NewLinkStore()).ServeHTTP(rec, req)

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
	req := httptest.NewRequest(
		http.MethodPost,
		"/healthz",
		nil,
	)

	rec := httptest.NewRecorder()
	newRouter(NewLinkStore()).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestUnknownRoute(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodGet,
		"/unknown",
		nil,
	)

	rec := httptest.NewRecorder()
	newRouter(NewLinkStore()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestCreateLink(t *testing.T) {
	store := NewLinkStore()
	handler := newRouter(store)

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

	if response.ShortURL != "http://localhost:8080/"+response.Code {
		t.Errorf("unexpected short URL: %q", response.ShortURL)
	}

	savedURL, ok := store.Get(response.Code)
	if !ok {
		t.Fatal("created link was not saved")
	}

	if savedURL != response.OriginalURL {
		t.Errorf("stored URL %q does not match response URL %q", savedURL, response.OriginalURL)
	}
}

func TestCreateLinkRejectsEmptyURL(t *testing.T) {
	handler := newRouter(NewLinkStore())

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
	store.Save("abc12345", "https://example.com/article")

	handler := newRouter(store)

	req := httptest.NewRequest(
		http.MethodGet,
		"/abc12345",
		nil,
	)

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
	handler := newRouter(NewLinkStore())

	req := httptest.NewRequest(
		http.MethodGet,
		"/unknown1",
		nil,
	)

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
				t.Errorf("validateURL(%q) error = %v, wantErr %v",
					tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestCreateLinkRejectsInvalidURL(t *testing.T) {
	store := NewLinkStore()
	handler := newRouter(store)

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

	if len(store.links) != 0 {
		t.Errorf("expected no links to be saved, got %d", len(store.links))
	}
}
