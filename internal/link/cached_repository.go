package link

import (
	"context"
	"log/slog"
	"time"
)

type Cache interface {
	Get(ctx context.Context, code string) (originalURL string, found bool, err error)
	Set(ctx context.Context, code, originalURL string, ttl time.Duration) error
}

// Only Get uses the cache; all other operations go to the source repository.
type CachedRepository struct {
	Repository
	cache   Cache
	logger  *slog.Logger
	ttl     time.Duration
	timeout time.Duration
}

func (s *CachedRepository) Get(ctx context.Context, code string) (string, error) {
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
	originalURL, err = s.Repository.Get(ctx, code)
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

func NewCachedRepository(repository Repository, cache Cache, logger *slog.Logger, ttl, timeout time.Duration) *CachedRepository {
	return &CachedRepository{Repository: repository, cache: cache, logger: logger, ttl: ttl, timeout: timeout}
}
