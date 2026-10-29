// Package app owns configuration and the application lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/httpapi"
	"github.com/eclipse-vlr/linkforge/internal/link"
	"github.com/eclipse-vlr/linkforge/internal/storage/postgres"
	rediscache "github.com/eclipse-vlr/linkforge/internal/storage/redis"
)

func Run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := postgres.Migrate(ctx, "file://migrations", cfg.DatabaseURL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	logger.Info("database migrations applied successfully")

	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

	store, err := postgres.NewStore(dbCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer store.Close()
	var repository link.Repository = store

	if cfg.RedisURL != "" {
		redisClient, err := rediscache.NewClient(cfg.RedisURL, cfg.RedisTimeout)
		if err != nil {
			return err
		}
		defer redisClient.Close()
		redisCtx, cancelRedis := context.WithTimeout(ctx, cfg.RedisTimeout)
		err = redisClient.Ping(redisCtx).Err()
		cancelRedis()
		if err != nil {
			logger.Warn("Redis unavailable; continuing with PostgreSQL", "error", err)
		} else {
			logger.Info("Redis connection ready")
		}
		repository = link.NewCachedRepository(store, rediscache.NewCache(redisClient, ""), logger, cfg.RedisCacheTTL, cfg.RedisTimeout)
	}

	handler := httpapi.LoggingMiddleware(logger, httpapi.NewRouter(repository, logger, cfg.BaseURL, cfg.CreateLinkAPIKey))
	server := httpapi.NewServer(cfg.HTTPAddr, handler)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	return serveHTTP(ctx, server, listener, logger, cfg.ShutdownTimeout)
}
