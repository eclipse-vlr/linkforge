package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stubLinkCache struct {
	get func(context.Context, string) (string, bool, error)
	set func(context.Context, string, string, time.Duration) error
}

func (c *stubLinkCache) Get(ctx context.Context, code string) (string, bool, error) {
	return c.get(ctx, code)
}
func (c *stubLinkCache) Set(ctx context.Context, code, original string, ttl time.Duration) error {
	return c.set(ctx, code, original, ttl)
}

type countedRepository struct {
	*LinkStore
	getCalls int
	getErr   error
}

func (s *countedRepository) Get(ctx context.Context, code string) (string, error) {
	s.getCalls++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.LinkStore.Get(ctx, code)
}

func TestCachedRepositoryGet(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	cacheErr := errors.New("cache unavailable")
	for _, tt := range []struct {
		name                     string
		hit                      bool
		readErr, writeErr, dbErr error
		missing, timeout         bool
		wantDB, wantSet          int
		wantErr                  error
	}{
		{name: "hit", hit: true},
		{name: "miss", wantDB: 1, wantSet: 1},
		{name: "read error", readErr: cacheErr, wantDB: 1, wantSet: 1},
		{name: "write error", writeErr: cacheErr, wantDB: 1, wantSet: 1},
		{name: "read deadline", timeout: true, wantDB: 1, wantSet: 1},
		{name: "missing", missing: true, wantDB: 1, wantErr: ErrLinkNotFound},
		{name: "database error", dbErr: storageErr, wantDB: 1, wantErr: storageErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &countedRepository{LinkStore: NewLinkStore(), getErr: tt.dbErr}
			if !tt.missing {
				if err := store.Save(context.Background(), "abc12345", "https://example.com"); err != nil {
					t.Fatal(err)
				}
			}
			setCalls := 0
			cache := &stubLinkCache{
				get: func(ctx context.Context, code string) (string, bool, error) {
					if code != "abc12345" {
						t.Fatalf("wrong cache key: %q", code)
					}
					if tt.timeout {
						<-ctx.Done()
						return "", false, ctx.Err()
					}
					return "https://example.com", tt.hit, tt.readErr
				},
				set: func(ctx context.Context, code, original string, ttl time.Duration) error {
					setCalls++
					if ctx.Err() != nil {
						t.Fatal("cache read timeout canceled cache write context")
					}
					if code != "abc12345" || original != "https://example.com" || ttl != 10*time.Minute {
						t.Fatal("incorrect cache fill")
					}
					return tt.writeErr
				},
			}
			var logs bytes.Buffer
			repository := &CachedLinkRepository{LinkRepository: store, cache: cache, logger: slog.New(slog.NewJSONHandler(&logs, nil)), ttl: 10 * time.Minute, timeout: 10 * time.Millisecond}
			original, err := repository.Get(context.Background(), "abc12345")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error %v, want %v", err, tt.wantErr)
			}
			if err == nil && original != "https://example.com" {
				t.Fatalf("unexpected URL %q", original)
			}
			if store.getCalls != tt.wantDB || setCalls != tt.wantSet {
				t.Fatalf("DB calls=%d, cache writes=%d", store.getCalls, setCalls)
			}
			if (tt.readErr != nil || tt.writeErr != nil || tt.timeout) && !strings.Contains(logs.String(), `"level":"WARN"`) {
				t.Fatal("cache failure was not logged")
			}
		})
	}
}

func TestCachedRedirectStillCountsClicks(t *testing.T) {
	store := &countedRepository{LinkStore: NewLinkStore()}
	value := ""
	cache := &stubLinkCache{
		get: func(context.Context, string) (string, bool, error) { return value, value != "", nil },
		set: func(ctx context.Context, code, original string, ttl time.Duration) error {
			value = original
			return nil
		},
	}
	repository := &CachedLinkRepository{LinkRepository: store, cache: cache, logger: newTestLogger(), ttl: time.Minute, timeout: time.Second}
	if err := repository.Save(context.Background(), "abc12345", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	handler := newRouter(repository, newTestLogger(), "https://sho.rt")
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/abc12345", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://example.com" {
			t.Fatal("redirect failed")
		}
	}
	stats, err := repository.GetStats(context.Background(), "abc12345")
	if err != nil || stats.ClickCount != 2 || store.getCalls != 1 {
		t.Fatalf("stats=%+v error=%v DB reads=%d", stats, err, store.getCalls)
	}
}
