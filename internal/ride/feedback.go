package ride

import (
	"context"
	"time"

	"ambigo-backend/internal/ids"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Feedback represents a post-ride rating submitted by a user for a driver.
// Table: feedback(id UUID PK, user_id TEXT, driver_id TEXT, ride_id TEXT, rating DOUBLE PRECISION, content TEXT, created_at TIMESTAMPTZ)
type Feedback struct {
	ID        string    `db:"id" json:"_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	DriverID  string    `db:"driver_id" json:"driver_id"`
	RideID    string    `db:"ride_id" json:"ride_id"`
	Rating    float64   `db:"rating" json:"rating" validate:"required,min=1,max=5"`
	Content   string    `db:"content" json:"content"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// FeedbackStore persists feedback in Postgres via pgxpool.
type FeedbackStore struct {
	pool *pgxpool.Pool
}

// NewFeedbackStore creates a FeedbackStore backed by the given pool.
func NewFeedbackStore(pool *pgxpool.Pool) *FeedbackStore {
	return &FeedbackStore{pool: pool}
}

// InsertFeedback inserts a new feedback row. A UUID v4 is generated if f.ID is empty and CreatedAt is set to now.
func (s *FeedbackStore) InsertFeedback(ctx context.Context, f *Feedback) error {
	if f.ID == "" {
		f.ID = ids.New()
	}
	f.CreatedAt = time.Now()
	_, err := s.pool.Exec(ctx,
		`INSERT INTO feedback(id, user_id, driver_id, ride_id, rating, content, created_at) VALUES($1::uuid,$2,$3,$4,$5,$6,$7)`,
		f.ID, f.UserID, f.DriverID, f.RideID, f.Rating, f.Content, f.CreatedAt,
	)
	return err
}

// ListAllFeedback returns all feedback rows. Returns empty (non-nil) slice when none.
func (s *FeedbackStore) ListAllFeedback(ctx context.Context) ([]Feedback, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, user_id, driver_id, ride_id, rating, content, created_at FROM feedback`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.UserID, &f.DriverID, &f.RideID, &f.Rating, &f.Content, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Feedback{}
	}
	return list, nil
}

// ListFeedback returns all feedback for a given driver_id (TEXT). Returns empty slice when none.
func (s *FeedbackStore) ListFeedback(ctx context.Context, driverID string) ([]Feedback, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, user_id, driver_id, ride_id, rating, content, created_at FROM feedback WHERE driver_id=$1`,
		driverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.UserID, &f.DriverID, &f.RideID, &f.Rating, &f.Content, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Feedback{}
	}
	return list, nil
}
