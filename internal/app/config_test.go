package app

import (
	"strings"
	"testing"
	"time"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CREATE_LINK_API_KEY", "HTTP_ADDR", "BASE_URL", "DATABASE_URL", "SHUTDOWN_TIMEOUT", "REDIS_URL", "REDIS_TIMEOUT", "REDIS_CACHE_TTL"} {
		t.Setenv(key, "")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		HTTPAddr: ":8080", BaseURL: "http://localhost:8080",
		DatabaseURL:     "postgres://linkforge:devpassword@localhost:5432/linkforge?sslmode=disable",
		ShutdownTimeout: 10 * time.Second,
		RedisTimeout:    100 * time.Millisecond,
		RedisCacheTTL:   10 * time.Minute,
	}
	if cfg != want {
		t.Fatalf("unexpected default config: %+v", cfg)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CREATE_LINK_API_KEY", "configured-test-key")
	t.Setenv("HTTP_ADDR", "[::1]:9090")
	t.Setenv("BASE_URL", "https://sho.rt/")
	t.Setenv("DATABASE_URL", "postgresql://user:password@db:5433/links?sslmode=require")
	t.Setenv("SHUTDOWN_TIMEOUT", "750ms")
	t.Setenv("REDIS_URL", "redis://localhost:6379/1")
	t.Setenv("REDIS_TIMEOUT", "250ms")
	t.Setenv("REDIS_CACHE_TTL", "5m")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CreateLinkAPIKey != "configured-test-key" || cfg.HTTPAddr != "[::1]:9090" || cfg.BaseURL != "https://sho.rt" ||
		cfg.DatabaseURL != "postgresql://user:password@db:5433/links?sslmode=require" || cfg.ShutdownTimeout != 750*time.Millisecond || cfg.RedisURL != "redis://localhost:6379/1" || cfg.RedisTimeout != 250*time.Millisecond || cfg.RedisCacheTTL != 5*time.Minute {
		t.Fatalf("unexpected overridden config: %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct{ key, value string }{
		{"HTTP_ADDR", "localhost"},
		{"HTTP_ADDR", ":0"},
		{"HTTP_ADDR", ":65536"},
		{"HTTP_ADDR", ":http"},
		{"BASE_URL", "example.com"},
		{"BASE_URL", "ftp://example.com"},
		{"BASE_URL", "https:///"},
		{"BASE_URL", "https://example.com/links"},
		{"BASE_URL", "https://example.com?token=secret"},
		{"BASE_URL", "https://example.com?"},
		{"BASE_URL", "https://example.com#"},
		{"BASE_URL", "https://user:secret@example.com"},
		{"BASE_URL", "https://example.com:65536"},
		{"BASE_URL", "https://example.com:"},
		{"DATABASE_URL", "not-a-url"},
		{"DATABASE_URL", "https://db/links"},
		{"DATABASE_URL", "postgres:///links"},
		{"DATABASE_URL", "postgres://user:secret@db:99999/links"},
		{"DATABASE_URL", "postgres://user:secret%zz@db/links"},
		{"SHUTDOWN_TIMEOUT", "10"},
		{"SHUTDOWN_TIMEOUT", "0s"},
		{"SHUTDOWN_TIMEOUT", "-1s"},
		{"REDIS_URL", "https://user:secret@localhost:6379"},
		{"REDIS_URL", "redis://user:secret%zz@localhost:6379"},
		{"REDIS_URL", "redis://localhost:6379/not-a-db"},
		{"REDIS_URL", "redis://localhost:0/0"},
		{"REDIS_URL", "redis://localhost:65536/0"},
		{"REDIS_URL", "redis://localhost:/0"},
		{"REDIS_URL", "redis://localhost:6379/-1"},
		{"REDIS_URL", "redis://localhost:6379/0?db=-1"},
		{"REDIS_URL", "redis://localhost:6379/0?pool_size=-1"},
		{"REDIS_URL", "redis://localhost:6379/0?pool_size=2147483648"},
		{"REDIS_URL", "redis://localhost:6379/0?pipeline_write_buffer_size=-1"},
		{"REDIS_URL", "redis://localhost:6379/0?db=0&db=1"},
		{"REDIS_URL", "redis://user:secret@localhost:6379/0?db=%zz"},
		{"REDIS_URL", "redis://user:secret@localhost:6379/0#fragment"},
		{"REDIS_URL", "redis://localhost:6379/0#"},
		{"REDIS_TIMEOUT", "0s"},
		{"REDIS_TIMEOUT", "-1ms"},
		{"REDIS_TIMEOUT", "100"},
		{"REDIS_CACHE_TTL", "0s"},
		{"REDIS_CACHE_TTL", "-1m"},
		{"REDIS_CACHE_TTL", "10"},
		{"REDIS_CACHE_TTL", "1ns"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"/"+tt.value, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tt.key, tt.value)
			_, err := loadConfig()
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("expected error naming %s, got %v", tt.key, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("config error exposed credentials")
			}
		})
	}
}

func TestRunRejectsConfigBeforeDatabaseStartup(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://user:password@127.0.0.1:1/unavailable")
	t.Setenv("SHUTDOWN_TIMEOUT", "-1s")
	if err := Run(newTestLogger()); err == nil || !strings.Contains(err.Error(), "load config: SHUTDOWN_TIMEOUT") {
		t.Fatalf("expected config error before database connection, got %v", err)
	}
}

func TestRunRejectsRedisOptionsBeforeDatabaseStartup(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://user:password@127.0.0.1:1/unavailable")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0?pool_size=-1")
	if err := Run(newTestLogger()); err == nil || !strings.Contains(err.Error(), "load config: REDIS_URL") {
		t.Fatalf("Run with invalid Redis options = %v, want REDIS_URL config error before database connection", err)
	}
}

func TestLoadConfigAcceptsValidRedisURLs(t *testing.T) {
	for _, connectionURL := range []string{
		"redis://localhost",
		"redis:///1",
		"redis://[::1]:1/0",
		"redis://localhost:65535/1",
		"rediss://user:password@cache:6379/1?db=2",
	} {
		t.Run(connectionURL, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("REDIS_URL", connectionURL)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.RedisURL != connectionURL {
				t.Fatalf("REDIS_URL changed: %q", cfg.RedisURL)
			}
		})
	}
}
