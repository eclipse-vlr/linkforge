package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisLinkCache struct {
	client *redis.Client
}

func (c *RedisLinkCache) Get(ctx context.Context, code string) (string, bool, error) {
	value, err := c.client.Get(ctx, "linkforge:link:"+code).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (c *RedisLinkCache) Set(ctx context.Context, code, originalURL string, ttl time.Duration) error {
	return c.client.Set(ctx, "linkforge:link:"+code, originalURL, ttl).Err()
}

func newRedisClient(connectionURL string, timeout time.Duration) (*redis.Client, error) {
	options, err := redis.ParseURL(connectionURL)
	if err != nil {
		// The URL may contain credentials; do not include it in the error.
		return nil, fmt.Errorf("invalid Redis connection URL")
	}
	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout
	options.PoolTimeout = timeout
	options.ContextTimeoutEnabled = true
	options.MaxRetries = -1
	return redis.NewClient(options), nil
}
