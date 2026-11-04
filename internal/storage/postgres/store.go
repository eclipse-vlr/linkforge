package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eclipse-vlr/linkforge/internal/link"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool              *pgxpool.Pool
	cancelConnections context.CancelFunc
}

func NewStore(
	ctx context.Context,
	connString string,
) (*Store, error) {
	config, err := parsePostgresPoolConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	// The caller's context bounds startup only. The store owns connections until
	// Close, including constructors that pgxpool starts with a background context.
	connections, cancelConnections := context.WithCancel(context.Background())
	stopStartupCancellation := context.AfterFunc(ctx, cancelConnections)
	defer stopStartupCancellation()
	config.ConnConfig.Tracer = &connectionLifecycle{lifetime: connections}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		cancelConnections()
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	err = pool.Ping(ctx)
	stopStartupCancellation()
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cancelConnections()
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{pool: pool, cancelConnections: cancelConnections}, nil
}

func (s *Store) Close() {
	// Stop only connection construction. The pool must still deliver SQL cancel
	// requests and join pgx cleanup for established connections.
	// The application calls Close only after draining HTTP requests.
	s.cancelConnections()
	s.pool.Close()
}

func (s *Store) Save(
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
		return link.ErrCodeConflict
	}

	return fmt.Errorf("insert link: %w", err)
}

func (s *Store) Get(
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
		return "", link.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select link: %w", err)
	}

	return originalURL, nil
}

func (s *Store) IncrementClicks(ctx context.Context, code string) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE links SET click_count = click_count + 1 WHERE code = $1`, code)
	if err != nil {
		return fmt.Errorf("increment link clicks: %w", err)
	}
	if result.RowsAffected() == 0 {
		return link.ErrNotFound
	}
	return nil
}

func (s *Store) GetStats(ctx context.Context, code string) (link.Stats, error) {
	var stats link.Stats
	err := s.pool.QueryRow(ctx,
		`SELECT code, click_count FROM links WHERE code = $1`, code,
	).Scan(&stats.Code, &stats.ClickCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return link.Stats{}, link.ErrNotFound
	}
	if err != nil {
		return link.Stats{}, fmt.Errorf("select link stats: %w", err)
	}
	return stats, nil
}

func parsePostgresPoolConfig(connString string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	// pgxpool starts a ticker with this period; non-positive values panic.
	if config.HealthCheckPeriod <= 0 {
		return nil, fmt.Errorf("pool_health_check_period must be a positive duration")
	}
	if config.ConnConfig.ConnectTimeout <= 0 {
		config.ConnConfig.ConnectTimeout = 5 * time.Second
	}
	return config, nil
}
