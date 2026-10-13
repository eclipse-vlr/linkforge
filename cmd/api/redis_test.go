package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRedisConnection(t *testing.T) {
	connectionURL := os.Getenv("TEST_REDIS_URL")
	if connectionURL == "" {
		t.Skip("set TEST_REDIS_URL to test a real Redis connection")
	}
	client, err := newRedisClient(connectionURL, 500*time.Millisecond)
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
	client, err := newRedisClient(connectionURL, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	suffix, err := generateCode()
	if err != nil {
		t.Fatal(err)
	}
	code := "test_" + suffix
	cache := &RedisLinkCache{client: client}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		cleanupClient, err := newRedisClient(connectionURL, time.Second)
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
