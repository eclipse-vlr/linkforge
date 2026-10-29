package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

func TestStatsReflectRedirects(t *testing.T) {
	store := NewLinkStore()
	if err := store.Save(context.Background(), "abc12345", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(store, newTestLogger(), "https://sho.rt", "")
	check := func(want int64) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/links/abc12345/stats", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected stats response: %d %s", rec.Code, rec.Body.String())
		}
		var stats link.Stats
		if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
			t.Fatal(err)
		}
		if stats.Code != "abc12345" || stats.ClickCount != want {
			t.Fatalf("stats = %+v, want count %d", stats, want)
		}
	}
	check(0)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/abc12345", nil))
		if rec.Code != http.StatusFound {
			t.Fatalf("redirect status = %d", rec.Code)
		}
	}
	check(3)
	check(3) // Reading statistics must not increment the counter.
}

type statsErrorRepository struct{ *LinkStore }

func (s *statsErrorRepository) GetStats(context.Context, string) (link.Stats, error) {
	return link.Stats{}, errors.New("private database error")
}

func TestStatsErrors(t *testing.T) {
	for _, tt := range []struct {
		name, method, path string
		store              link.Repository
		status             int
	}{
		{"missing", http.MethodGet, "/api/v1/links/missing/stats", NewLinkStore(), http.StatusNotFound},
		{"method", http.MethodPost, "/api/v1/links/abc12345/stats", NewLinkStore(), http.StatusMethodNotAllowed},
		{"extra path", http.MethodGet, "/api/v1/links/abc12345/stats/extra", NewLinkStore(), http.StatusNotFound},
		{"storage error", http.MethodGet, "/api/v1/links/abc12345/stats", &statsErrorRepository{NewLinkStore()}, http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			rec := httptest.NewRecorder()
			NewRouter(tt.store, slog.New(slog.NewJSONHandler(&logs, nil)), "https://sho.rt", "").ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if tt.status == http.StatusMethodNotAllowed && rec.Header().Get("Allow") != "GET" {
				t.Fatal("missing Allow header")
			}
			if strings.Contains(rec.Body.String(), "private database error") {
				t.Fatal("database details exposed in response")
			}
			if tt.status == http.StatusInternalServerError && !strings.Contains(logs.String(), "private database error") {
				t.Fatal("database error was not logged")
			}
		})
	}
}
