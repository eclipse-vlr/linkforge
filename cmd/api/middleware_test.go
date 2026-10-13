package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLoggingMiddleware(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		status  int
	}{
		{"empty response", func(w http.ResponseWriter, r *http.Request) {}, http.StatusOK},
		{"implicit OK", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hello"))
		}, http.StatusOK},
		{"created", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"code":"abc12345"}`))
		}, http.StatusCreated},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.com", http.StatusFound)
		}, http.StatusFound},
		{"not found", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}, http.StatusNotFound},
		{"internal error", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}, http.StatusInternalServerError},
		{"repeated WriteHeader", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("created"))
		}, http.StatusCreated},
		{"Write before WriteHeader", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hello"))
			w.WriteHeader(http.StatusInternalServerError)
		}, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			req := httptest.NewRequest(http.MethodGet, "/abc12345?token=secret", nil)
			baseline := httptest.NewRecorder()
			tt.handler.ServeHTTP(baseline, req)
			rec := httptest.NewRecorder()
			loggingMiddleware(logger, tt.handler).ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if rec.Code != baseline.Code || rec.Body.String() != baseline.Body.String() ||
				!reflect.DeepEqual(rec.Result().Header, baseline.Result().Header) {
				t.Fatal("middleware changed the HTTP response")
			}

			var entry struct {
				Level      string   `json:"level"`
				Message    string   `json:"msg"`
				Method     string   `json:"method"`
				Path       string   `json:"path"`
				Status     int      `json:"status"`
				DurationMS *float64 `json:"duration_ms"`
			}
			decoder := json.NewDecoder(&logs)
			if err := decoder.Decode(&entry); err != nil {
				t.Fatalf("decode log: %v", err)
			}
			if entry.Level != "INFO" || entry.Message != "HTTP request completed" ||
				entry.Method != http.MethodGet || entry.Path != "/abc12345" || entry.Status != tt.status {
				t.Fatalf("unexpected log: %+v", entry)
			}
			if entry.DurationMS == nil || *entry.DurationMS < 0 {
				t.Fatalf("invalid duration: %v", entry.DurationMS)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("expected exactly one log entry, got %v", err)
			}
		})
	}
}

func TestResponseWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	writer := &responseWriter{ResponseWriter: rec}
	if writer.Unwrap() != rec {
		t.Fatal("Unwrap did not return the underlying writer")
	}
}
