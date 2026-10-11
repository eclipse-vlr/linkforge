package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrLinkNotFound = errors.New("link not found")
	ErrCodeConflict = errors.New("short code already exists")
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(
	ctx context.Context,
	connString string,
) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	s.pool.Close()
}

func (s *PostgresStore) Save(
	ctx context.Context,
	code, originalURL string,
) error {
	_, err := s.pool.Exec(
		ctx,
		`INSERT INTO links (code, original_url)
		 VALUES ($1, $2)`,
		code,
		originalURL,
	)

	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "links_code_key" {
		return ErrCodeConflict
	}

	return fmt.Errorf("insert link: %w", err)
}

func (s *PostgresStore) Get(
	ctx context.Context,
	code string,
) (string, error) {
	var originalURL string

	err := s.pool.QueryRow(
		ctx,
		`SELECT original_url
		 FROM links
		 WHERE code = $1`,
		code,
	).Scan(&originalURL)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLinkNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select link: %w", err)
	}

	return originalURL, nil
}
