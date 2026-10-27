package main

import (
	"context"
	"strings"
	"testing"
)

func TestPostgresStoreRejectsInvalidPoolPeriod(t *testing.T) {
	for _, period := range []string{"0s", "-1s"} {
		t.Run(period, func(t *testing.T) {
			connectionURL := "postgres://user:secret@127.0.0.1:1/unavailable?pool_health_check_period=" + period
			store, err := NewPostgresStore(context.Background(), connectionURL)
			if store != nil {
				store.Close()
				t.Fatal("invalid pool period returned a store")
			}
			if err == nil || !strings.Contains(err.Error(), "pool_health_check_period") {
				t.Fatalf("expected pool period error before connecting, got %v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("pool configuration error exposed credentials")
			}
		})
	}
}
