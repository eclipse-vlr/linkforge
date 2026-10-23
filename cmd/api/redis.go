package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisLinkCache struct {
	client    *redis.Client
	keyPrefix string
}

func (c *RedisLinkCache) key(code string) string {
	if c.keyPrefix != "" {
		return c.keyPrefix + code
	}
	return "linkforge:link:" + code
}

func (c *RedisLinkCache) Get(ctx context.Context, code string) (string, bool, error) {
	value, err := c.client.Get(ctx, c.key(code)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (c *RedisLinkCache) Set(ctx context.Context, code, originalURL string, ttl time.Duration) error {
	return c.client.Set(ctx, c.key(code), originalURL, ttl).Err()
}

func newRedisClient(connectionURL string, timeout time.Duration) (*redis.Client, error) {
	options, err := parseRedisOptions(connectionURL)
	if err != nil {
		return nil, err
	}
	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout
	options.PoolTimeout = timeout
	options.ContextTimeoutEnabled = true
	options.MaxRetries = -1
	return redis.NewClient(options), nil
}

func parseRedisOptions(connectionURL string) (*redis.Options, error) {
	// Never include the URL or parsing errors: they can contain credentials.
	parsed, err := url.Parse(connectionURL)
	if err != nil || (parsed.Scheme != "redis" && parsed.Scheme != "rediss") {
		return nil, fmt.Errorf("REDIS_URL must use the redis or rediss scheme")
	}
	if (parsed.Port() != "" && !validPort(parsed.Port())) || strings.HasSuffix(parsed.Host, ":") || strings.Contains(connectionURL, "#") {
		return nil, fmt.Errorf("REDIS_URL must have a valid port and no fragment")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL must contain valid query parameters")
	}
	// Pool and buffer options can panic in NewClient. Keep those settings owned
	// by the application; the URL only selects the connection and database.
	for name, values := range query {
		if name != "db" || len(values) != 1 {
			return nil, fmt.Errorf("REDIS_URL supports only one optional db query parameter")
		}
	}
	options, err := redis.ParseURL(connectionURL)
	if err != nil || options.DB < 0 {
		return nil, fmt.Errorf("REDIS_URL must be a valid redis or rediss connection URL with a non-negative database number")
	}
	return options, nil
}
