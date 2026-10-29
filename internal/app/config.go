package app

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	rediscache "github.com/eclipse-vlr/linkforge/internal/storage/redis"
)

type Config struct {
	CreateLinkAPIKey string
	HTTPAddr         string
	BaseURL          string
	DatabaseURL      string
	ShutdownTimeout  time.Duration
	RedisURL         string
	RedisTimeout     time.Duration
	RedisCacheTTL    time.Duration
}

func loadConfig() (Config, error) {
	cfg := Config{
		CreateLinkAPIKey: os.Getenv("CREATE_LINK_API_KEY"),
		HTTPAddr:         envOrDefault("HTTP_ADDR", ":8080"),
		BaseURL:          envOrDefault("BASE_URL", "http://localhost:8080"),
		DatabaseURL:      envOrDefault("DATABASE_URL", "postgres://linkforge:devpassword@localhost:5432/linkforge?sslmode=disable"),
		RedisURL:         os.Getenv("REDIS_URL"),
	}
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil || !validPort(port) {
		return Config{}, fmt.Errorf("HTTP_ADDR must be host:port with a port from 1 to 65535")
	}

	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" {
		return Config{}, fmt.Errorf("BASE_URL must be an absolute HTTP or HTTPS URL with a hostname")
	}
	if base.User != nil || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.ForceQuery || strings.Contains(cfg.BaseURL, "#") {
		return Config{}, fmt.Errorf("BASE_URL must not contain credentials, a path prefix, query, or fragment")
	}
	if (base.Port() != "" && !validPort(base.Port())) || strings.HasSuffix(base.Host, ":") {
		return Config{}, fmt.Errorf("BASE_URL port must be from 1 to 65535")
	}
	base.Path, base.RawPath = "", ""
	cfg.BaseURL = base.String()

	// Do not include the supplied database URL in errors: it may contain a password.
	database, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (database.Scheme != "postgres" && database.Scheme != "postgresql") || database.Hostname() == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must be a postgres or postgresql URL with a hostname")
	}
	if (database.Port() != "" && !validPort(database.Port())) || strings.HasSuffix(database.Host, ":") || strings.Contains(cfg.DatabaseURL, "#") {
		return Config{}, fmt.Errorf("DATABASE_URL must have a valid port and no fragment")
	}

	cfg.ShutdownTimeout, err = time.ParseDuration(envOrDefault("SHUTDOWN_TIMEOUT", "10s"))
	if err != nil || cfg.ShutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration, for example 10s")
	}
	if cfg.RedisURL != "" {
		if _, err := rediscache.ParseOptions(cfg.RedisURL); err != nil {
			return Config{}, err
		}
	}
	cfg.RedisTimeout, err = time.ParseDuration(envOrDefault("REDIS_TIMEOUT", "100ms"))
	if err != nil || cfg.RedisTimeout <= 0 {
		return Config{}, fmt.Errorf("REDIS_TIMEOUT must be a positive duration, for example 100ms")
	}
	cfg.RedisCacheTTL, err = time.ParseDuration(envOrDefault("REDIS_CACHE_TTL", "10m"))
	if err != nil || cfg.RedisCacheTTL < time.Millisecond {
		return Config{}, fmt.Errorf("REDIS_CACHE_TTL must be at least 1ms, for example 10m")
	}
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}
