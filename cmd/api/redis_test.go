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
