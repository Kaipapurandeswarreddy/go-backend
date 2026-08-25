package config

import (
	"context"
	"fmt"
	"time"

	"ambigo-backend/internal/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresConfig holds Postgres-specific pool tuning.
type PostgresConfig struct {
	DatabaseURL       string
	MaxOpenConns      int32
	MinOpenConns      int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

// InitPostgres creates a pgxpool.Pool, pings the DB, and returns it.
// Caller must call pool.Close() on shutdown (no context needed).
func InitPostgres(ctx context.Context, cfg PostgresConfig) (*pgxpool.Pool, error) {
	logger.Log.Info().Str("host", maskDSN(cfg.DatabaseURL)).Msg("Connecting to PostgreSQL...")

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		poolCfg.MaxConns = cfg.MaxOpenConns
	}
	if cfg.MinOpenConns > 0 {
		poolCfg.MinConns = cfg.MinOpenConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pg pool: %w", err)
	}

	// Ping with timeout
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	logger.Log.Info().
		Int32("max_conns", poolCfg.MaxConns).
		Int32("min_conns", poolCfg.MinConns).
		Msg("PostgreSQL connected")

	return pool, nil
}

// maskDSN hides password for logging.
func maskDSN(dsn string) string {
	// Very small helper: hide between "://" and "@" if present
	// postgres://user:pass@host/db -> postgres://user:***@host/db
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == ':' && i+2 < len(dsn) && dsn[i+1] == '/' && dsn[i+2] == '/' {
			// found ://
			rest := dsn[i+3:]
			at := -1
			for j, c := range rest {
				if c == '@' {
					at = j
					break
				}
			}
			if at >= 0 {
				// find ':' before '@' (password separator)
				colon := -1
				for j := 0; j < at; j++ {
					if rest[j] == ':' {
						colon = j
					}
				}
				if colon >= 0 {
					return dsn[:i+3] + rest[:colon+1] + "***" + rest[at:]
				}
			}
			break
		}
	}
	return dsn
}
