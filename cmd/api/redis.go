package main

import (
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

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
