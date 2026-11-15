// Package postgres implements the persistence interfaces of internal/store on
// top of pgx. Only the cmd/* binaries import it.
package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the Postgres-backed store: a connection pool plus a logger.
type DB struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
}

// New opens a pool for dsn and pings it, so a bad URL or a database that is
// down fails here rather than on the first query. A maxConns of 0 or less keeps
// pgx's default pool size. A nil log falls back to slog.Default().
func New(ctx context.Context, dsn string, maxConns int32, log *slog.Logger) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &DB{Pool: pool, Log: log}, nil
}

// Close releases every pooled connection.
func (d *DB) Close() {
	d.Pool.Close()
}
