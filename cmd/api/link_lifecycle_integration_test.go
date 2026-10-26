package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Observe URL lookups without replacing any real PostgreSQL operation.
type observedLinkRepository struct {
	LinkRepository
	urlLookups atomic.Int32
}

func (s *observedLinkRepository) Get(ctx context.Context, code string) (string, error) {
	s.urlLookups.Add(1)
	return s.LinkRepository.Get(ctx, code)
}

func TestLinkLifecycleIntegration(t *testing.T) {
	databaseURL, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("set both TEST_DATABASE_URL and TEST_REDIS_URL for the HTTP lifecycle integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	suffix, err := generateCode()
	if err != nil {
		t.Fatal(err)
	}
	schema := "http_test_" + suffix
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean schema: %v", err)
		}
	}()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", quotedSchema)
	// Both migrations and the repository must accept the same pool options.
	query.Set("pool_max_conns", "4")
	query.Set("pool_min_conns", "1")
	query.Set("pool_max_conn_lifetime", "5m")
	parsed.RawQuery = query.Encode()
	if err := applyMigrations(ctx, "file://../../migrations", parsed.String()); err != nil {
		t.Fatal(err)
	}
	store, err := NewPostgresStore(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.pool.Config().MaxConns; got != 4 {
		t.Fatalf("pool MaxConns = %d, want 4", got)
	}

	// A separate client can clean up owned keys even after the cache client closes.
	redisAdmin, err := newRedisClient(redisURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer redisAdmin.Close()
	if err := redisAdmin.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	cacheClient, err := newRedisClient(redisURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cacheClient.Close()
	cache := &RedisLinkCache{client: cacheClient, keyPrefix: "linkforge:test:" + suffix + ":"}
	var code string
	defer func() {
		if code == "" {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		if err := redisAdmin.Del(cleanupCtx, cache.key(code)).Err(); err != nil {
			t.Errorf("clean cache key: %v", err)
		}
	}()
	observed := &observedLinkRepository{LinkRepository: store}
	repository := &CachedLinkRepository{
		LinkRepository: observed, cache: cache, logger: newTestLogger(),
		ttl: time.Minute, timeout: time.Second,
	}
	const baseURL = "https://sho.rt"
	handler := loggingMiddleware(newTestLogger(), newRouter(repository, newTestLogger(), baseURL))
	server := httptest.NewUnstartedServer(handler)
	server.Config = newHTTPServer("", handler)
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer client.CloseIdleConnections()
	request := func(method, path, body string, wantStatus int) (http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != wantStatus {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.StatusCode, wantStatus, data)
		}
		return response.Header, data
	}
	checkStats := func(want int64) {
		t.Helper()
		_, data := request(http.MethodGet, "/api/v1/links/"+code+"/stats", "", http.StatusOK)
		var stats LinkStats
		if err := json.Unmarshal(data, &stats); err != nil {
			t.Fatal(err)
		}
		if stats.Code != code || stats.ClickCount != want {
			t.Fatalf("HTTP stats = %+v, want %s/%d", stats, code, want)
		}
		persisted, err := store.GetStats(ctx, code)
		if err != nil || persisted != stats {
			t.Fatalf("persisted stats = %+v, %v; HTTP stats = %+v", persisted, err, stats)
		}
	}
	const originalURL = "https://example.invalid/articles/go?from=integration"
	_, data := request(http.MethodPost, "/api/v1/links", `{"url":"`+originalURL+`"}`, http.StatusCreated)
	var created CreateLinkResponse
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	code = created.Code
	if len(code) != 8 || created.ShortURL != baseURL+"/"+code || created.OriginalURL != originalURL {
		t.Fatalf("unexpected create response: %+v", created)
	}
	if persisted, err := store.Get(ctx, code); err != nil || persisted != originalURL {
		t.Fatalf("persisted URL = %q, %v", persisted, err)
	}
	checkStats(0)
	if _, found, err := cache.Get(ctx, code); err != nil || found {
		t.Fatalf("expected empty cache after creation, found=%t err=%v", found, err)
	}
	redirect := func() {
		t.Helper()
		headers, _ := request(http.MethodGet, "/"+code, "", http.StatusFound)
		if headers.Get("Location") != originalURL {
			t.Fatalf("redirect Location = %q", headers.Get("Location"))
		}
	}
	redirect()
	if observed.urlLookups.Load() != 1 {
		t.Fatal("first redirect did not look up PostgreSQL")
	}
	if value, found, err := cache.Get(ctx, code); err != nil || !found || value != originalURL {
		t.Fatalf("cache was not populated: %q, %t, %v", value, found, err)
	}
	if ttl, err := redisAdmin.PTTL(ctx, cache.key(code)).Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("cache TTL = %s, %v", ttl, err)
	}
	checkStats(1)
	redirect()
	if observed.urlLookups.Load() != 1 {
		t.Fatal("cached redirect performed another PostgreSQL URL lookup")
	}
	checkStats(2)
	checkStats(2) // Reading statistics must not count as another click.

	// A real client failure must preserve redirect and counter behavior.
	if err := cacheClient.Close(); err != nil {
		t.Fatal(err)
	}
	redirect()
	if observed.urlLookups.Load() != 2 {
		t.Fatal("cache failure did not fall back to PostgreSQL")
	}
	checkStats(3)
	request(http.MethodGet, "/missing_"+suffix, "", http.StatusNotFound)
	request(http.MethodGet, "/api/v1/links/missing_"+suffix+"/stats", "", http.StatusNotFound)
	checkStats(3)
}
