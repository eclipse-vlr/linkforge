package main

import (
	"context"
	"log/slog"
	"time"
)

type LinkCache interface {
	Get(ctx context.Context, code string) (originalURL string, found bool, err error)
	Set(ctx context.Context, code, originalURL string, ttl time.Duration) error
}

// Only Get uses the cache; all other operations go to the source repository.
type CachedLinkRepository struct {
	LinkRepository
	cache   LinkCache
	logger  *slog.Logger
	ttl     time.Duration
	timeout time.Duration
}

func (s *CachedLinkRepository) Get(ctx context.Context, code string) (string, error) {
	cacheCtx, cancel := context.WithTimeout(ctx, s.timeout)
	originalURL, found, err := s.cache.Get(cacheCtx, code)
	cancel()
	if err == nil && found {
		return originalURL, nil
	}
	if err != nil {
		s.logger.WarnContext(ctx, "failed to read link cache", "code", code, "error", err)
	}
	// Use the original context: a Redis timeout must not cancel the DB lookup.
	originalURL, err = s.LinkRepository.Get(ctx, code)
	if err != nil {
		return "", err
	}
	cacheCtx, cancel = context.WithTimeout(ctx, s.timeout)
	err = s.cache.Set(cacheCtx, code, originalURL, s.ttl)
	cancel()
	if err != nil {
		s.logger.WarnContext(ctx, "failed to write link cache", "code", code, "error", err)
	}
	return originalURL, nil
}
