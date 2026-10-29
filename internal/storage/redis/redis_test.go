package rediscache

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

func TestNewRedisClientRejectsInvalidURL(t *testing.T) {
	for _, connectionURL := range []string{
		"redis://user:secret@localhost:6379/0?pool_size=-1",
		"redis://localhost:6379/0?pool_size=2147483648",
		"redis://localhost:6379/0?pool_size=10",
		"redis://localhost:6379/0?pipeline_write_buffer_size=-1",
		"redis://localhost:6379/0?db=0&db=1",
		"redis://localhost:6379/0?db=0&d%62=1",
		"redis://localhost:6379/0?db=%zz",
		"redis://localhost:6379/0?secret=value",
		"redis://localhost:6379/-1",
	} {
		t.Run(connectionURL, func(t *testing.T) {
			client, err := NewClient(connectionURL, time.Second)
			if client != nil {
				_ = client.Close()
				t.Fatal("NewClient with invalid URL returned a client")
			}
			if err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
				t.Fatalf("NewClient(%q) = %v, want REDIS_URL error", connectionURL, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("NewClient exposed credentials in error: %v", err)
			}
		})
	}
}

func TestRedisConnection(t *testing.T) {
	connectionURL := os.Getenv("TEST_REDIS_URL")
	if connectionURL == "" {
		t.Skip("set TEST_REDIS_URL to test a real Redis connection")
	}
	client, err := NewClient(connectionURL, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if pong, err := client.Ping(ctx).Result(); err != nil || pong != "PONG" {
		t.Fatalf("Redis ping = %q, %v", pong, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx).Err(); err == nil {
		t.Fatal("expected ping to fail after client close")
	}
}

func TestRedisLinkCache(t *testing.T) {
	connectionURL := os.Getenv("TEST_REDIS_URL")
	if connectionURL == "" {
		t.Skip("set TEST_REDIS_URL to test Redis cache")
	}
	client, err := NewClient(connectionURL, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	suffix, err := link.GenerateCode()
	if err != nil {
		t.Fatal(err)
	}
	code := "test_" + suffix
	cache := &Cache{client: client}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		cleanupClient, err := NewClient(connectionURL, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer cleanupClient.Close()
		if err := cleanupClient.Del(cleanupCtx, "linkforge:link:"+code).Err(); err != nil {
			t.Error(err)
		}
	})
	if _, found, err := cache.Get(ctx, code); err != nil || found {
		t.Fatalf("expected miss, found=%v err=%v", found, err)
	}
	if err := cache.Set(ctx, code, "https://example.com", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if value, found, err := cache.Get(ctx, code); err != nil || !found || value != "https://example.com" {
		t.Fatalf("expected hit, value=%q found=%v err=%v", value, found, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, found, err := cache.Get(ctx, code)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache entry did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
