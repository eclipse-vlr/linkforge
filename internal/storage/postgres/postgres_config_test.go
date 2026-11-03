package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPoolConnectionTimeout(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  time.Duration
	}{
		{"", 5 * time.Second},
		{"&connect_timeout=0", 5 * time.Second},
		{"&connect_timeout=30", 30 * time.Second},
	} {
		config, err := parsePostgresPoolConfig("postgres://user:secret@localhost/db?sslmode=disable" + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		if config.ConnConfig.ConnectTimeout != tc.want {
			t.Fatalf("%s: got %s, want %s", tc.query, config.ConnConfig.ConnectTimeout, tc.want)
		}
	}
}

func TestPostgresStoreRejectsInvalidPoolPeriod(t *testing.T) {
	for _, period := range []string{"0s", "-1s"} {
		t.Run(period, func(t *testing.T) {
			connectionURL := "postgres://user:secret@127.0.0.1:1/unavailable?pool_health_check_period=" + period
			store, err := NewStore(context.Background(), connectionURL)
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
