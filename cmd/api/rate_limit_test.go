package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func rateLimitRequest(handler http.Handler, address string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	req.RemoteAddr = address
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestCreateLinkLimiterSlidingWindow(t *testing.T) {
	limiter := newCreateLinkLimiter()
	now := time.Unix(1_000, 0)
	limiter.now = func() time.Time { return now }
	handler := limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for i := range createLinkLimit {
		now = time.Unix(1_000+int64(i*6), 0)
		if rec := rateLimitRequest(handler, "192.0.2.1:"+strconv.Itoa(10_000+i)); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	now = time.Unix(1_059, 0).Add(100 * time.Millisecond)
	rec := rateLimitRequest(handler, "192.0.2.1:20000")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("expected 429 with rounded Retry-After=1, got %d, %v", rec.Code, rec.Header())
	}
	// A rejection must not move the first request's expiry.
	now = time.Unix(1_060, 0)
	if rec := rateLimitRequest(handler, "192.0.2.1:20001"); rec.Code != http.StatusNoContent {
		t.Fatalf("request at exact expiry: %d", rec.Code)
	}
	rec = rateLimitRequest(handler, "192.0.2.1:20002")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "6" {
		t.Fatalf("expected remaining rolling quota, got %d, %v", rec.Code, rec.Header())
	}
	if rec := rateLimitRequest(handler, "192.0.2.2:20003"); rec.Code != http.StatusNoContent {
		t.Fatal("another IP shared the exhausted quota")
	}
}

func TestCreateLinkLimiterClientIdentity(t *testing.T) {
	for _, tc := range []struct{ name, first, next string }{
		{"IPv4 mapped", "192.0.2.1:1234", "[::ffff:192.0.2.1]:5678"},
		{"IPv6 spelling", "[2001:db8::1]:1234", "[2001:0db8:0:0:0:0:0:1]:5678"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limiter := newCreateLinkLimiter()
			handler := limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			for range createLinkLimit {
				if rec := rateLimitRequest(handler, tc.first); rec.Code != http.StatusNoContent {
					t.Fatal(rec.Code)
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
			req.RemoteAddr = tc.next
			req.Header.Set("X-Forwarded-For", "203.0.113.2")
			req.Header.Set("X-Real-IP", "203.0.113.3")
			req.Header.Set("Forwarded", "for=203.0.113.4")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("identity bypass: %d", rec.Code)
			}
		})
	}
}

func TestCreateLinkLimiterConcurrentAdmission(t *testing.T) {
	limiter := newCreateLinkLimiter()
	frozen := time.Now()
	limiter.now = func() time.Time { return frozen }
	var admitted, rejected, unexpected atomic.Int32
	handler := limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		admitted.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	var wg sync.WaitGroup
	start := make(chan struct{})
	const requests = 100
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := rateLimitRequest(handler, "192.0.2.1:"+strconv.Itoa(10_000+i))
			if rec.Code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") == "60" {
				rejected.Add(1)
			} else if rec.Code != http.StatusNoContent {
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if admitted.Load() != createLinkLimit || rejected.Load() != requests-createLinkLimit || unexpected.Load() != 0 {
		t.Fatalf("admitted=%d, rejected=%d, unexpected=%d", admitted.Load(), rejected.Load(), unexpected.Load())
	}
}

func TestCreateLinkLimiterBoundedClientsAndExpiry(t *testing.T) {
	limiter := newCreateLinkLimiter()
	limiter.maxClients = 2
	now := time.Unix(1_000, 0)
	limiter.now = func() time.Time { return now }
	handler := limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	check := func(address string, status int, retry string) {
		t.Helper()
		rec := rateLimitRequest(handler, address)
		if rec.Code != status || rec.Header().Get("Retry-After") != retry {
			t.Fatalf("%s: %d, %v", address, rec.Code, rec.Header())
		}
	}
	check("192.0.2.1:1234", http.StatusNoContent, "")
	now = now.Add(10 * time.Second)
	check("192.0.2.2:1234", http.StatusNoContent, "")
	now = now.Add(10 * time.Second)
	check("192.0.2.1:5678", http.StatusNoContent, "")
	check("192.0.2.3:1234", http.StatusServiceUnavailable, "50")
	if len(limiter.clients) != 2 {
		t.Fatal("client capacity exceeded")
	}
	now = now.Add(50 * time.Second)
	check("192.0.2.3:1234", http.StatusNoContent, "")
	if len(limiter.clients) != 2 || limiter.activity.Len() != 2 {
		t.Fatal("expired client not removed or active client lost")
	}
	for range createLinkLimit - 1 {
		check("192.0.2.1:1234", http.StatusNoContent, "")
	}
	check("192.0.2.1:1234", http.StatusTooManyRequests, "10")
	now = now.Add(time.Minute)
	check("192.0.2.4:1234", http.StatusNoContent, "")
	if len(limiter.clients) != 1 || limiter.activity.Len() != 1 {
		t.Fatal("idle clients were not reclaimed")
	}
}

func TestRouterCreateLinkRateLimit(t *testing.T) {
	store := NewLinkStore()
	if err := store.Save(context.Background(), "existing", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := loggingMiddleware(logger, newRouter(store, logger, "https://sho.rt"))
	request := func(method, path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"url":"https://example.com"}`))
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d: %s", method, path, rec.Code, rec.Body)
		}
		return rec
	}
	request(http.MethodGet, "/api/v1/links", http.StatusMethodNotAllowed)
	for range createLinkLimit {
		request(http.MethodPost, "/api/v1/links", http.StatusCreated)
	}
	logs.Reset()
	rec := request(http.MethodPost, "/api/v1/links", http.StatusTooManyRequests)
	if len(store.links) != createLinkLimit+1 {
		t.Fatal("rejected request changed storage")
	}
	if retry, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || retry < 1 || retry > 60 {
		t.Fatalf("invalid Retry-After: %v", rec.Header())
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["status"] != float64(http.StatusTooManyRequests) {
		t.Fatalf("wrong logged status: %v", entry)
	}
	request(http.MethodGet, "/healthz", http.StatusOK)
	request(http.MethodGet, "/existing", http.StatusFound)
	request(http.MethodGet, "/api/v1/links/existing/stats", http.StatusOK)
}

func TestCreateLinkLimiterCountsInvalidAttempts(t *testing.T) {
	handler := newRouter(NewLinkStore(), newTestLogger(), "https://sho.rt")
	for range createLinkLimit {
		if rec := rateLimitRequest(handler, "192.0.2.1:1234"); rec.Code != http.StatusBadRequest {
			t.Fatal(rec.Code)
		}
	}
	if rec := rateLimitRequest(handler, "192.0.2.1:5678"); rec.Code != http.StatusTooManyRequests {
		t.Fatal(rec.Code)
	}
}

func TestCreateLinkLimiterInvalidPeer(t *testing.T) {
	limiter := newCreateLinkLimiter()
	handler := limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid peer reached handler") }))
	for _, address := range []string{"", "not-an-ip:1234", "192.0.2.1", "[::1]"} {
		if rec := rateLimitRequest(handler, address); rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: %d", address, rec.Code)
		}
	}
	if len(limiter.clients) != 0 {
		t.Fatal("invalid peers consumed capacity")
	}
}
