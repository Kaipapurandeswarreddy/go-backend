package admin

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CounterStore persists monotonic integer counters in Postgres.
//
// Table: counters(id TEXT PRIMARY KEY, value INT NOT NULL)
// Uses INSERT ... ON CONFLICT upsert to atomically increment.
type CounterStore struct {
	pool *pgxpool.Pool
}

// NewCounterStore creates a CounterStore backed by a pgxpool.Pool.
func NewCounterStore(pool *pgxpool.Pool) *CounterStore {
	return &CounterStore{pool: pool}
}

// IncrementCounter atomically increments the counter identified by id.
// If the row does not exist it is created with value 1.
func (s *CounterStore) IncrementCounter(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO counters(id, value) VALUES($1, 1) ON CONFLICT (id) DO UPDATE SET value = counters.value + 1`,
		id,
	)
	return err
}

// GetCounter returns the current value for id, or 0 if no row exists.
func (s *CounterStore) GetCounter(ctx context.Context, id string) (int, error) {
	var value int
	err := s.pool.QueryRow(ctx, `SELECT value FROM counters WHERE id=$1`, id).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return value, nil
}
