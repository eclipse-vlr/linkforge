package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type clickRepository struct {
	*LinkStore
	increment func(context.Context, string) error
}

func (s *clickRepository) IncrementClicks(ctx context.Context, code string) error {
	return s.increment(ctx, code)
}

func TestRedirectCountsClicks(t *testing.T) {
	store := NewLinkStore()
	if err := store.Save(context.Background(), "abc12345", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(store, newTestLogger(), "https://sho.rt", "")
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/abc12345", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://example.com" {
			t.Fatalf("unexpected redirect: %d %s", rec.Code, rec.Header().Get("Location"))
		}
	}
	if store.clicks["abc12345"] != 2 {
		t.Fatalf("expected two clicks, got %d", store.clicks["abc12345"])
	}
}

func TestRedirectSurvivesStatsFailure(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "database error"
		if timeout {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			memory := NewLinkStore()
			if err := memory.Save(context.Background(), "abc12345", "https://example.com"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			store := &clickRepository{LinkStore: memory, increment: func(ctx context.Context, code string) error {
				calls++
				if code != "abc12345" {
					t.Errorf("unexpected code %q", code)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 500*time.Millisecond {
					t.Fatal("statistics must have a deadline of at most 500ms")
				}
				if timeout {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(2 * time.Second):
						t.Fatal("statistics context was not canceled")
					}
				}
				return errors.New("database unavailable")
			}}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			rec := httptest.NewRecorder()
			NewRouter(store, logger, "https://sho.rt", "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/abc12345", nil))
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://example.com" || calls != 1 {
				t.Fatalf("stats failure changed redirect: status=%d calls=%d", rec.Code, calls)
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["level"] != "ERROR" || entry["code"] != "abc12345" || entry["error"] == nil {
				t.Fatalf("missing statistics error log: %v", entry)
			}
		})
	}
}

func TestNonRedirectDoesNotCountClicks(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/unknown", http.StatusNotFound},
		{http.MethodGet, "/invalid/path", http.StatusNotFound},
		{http.MethodPost, "/abc12345", http.StatusMethodNotAllowed},
		{http.MethodGet, "/healthz", http.StatusOK},
	} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			store := &clickRepository{LinkStore: NewLinkStore(), increment: func(context.Context, string) error {
				t.Error("unexpected statistics update")
				return nil
			}}
			rec := httptest.NewRecorder()
			NewRouter(store, newTestLogger(), "https://sho.rt", "").ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d", rec.Code, tt.status)
			}
		})
	}
}
