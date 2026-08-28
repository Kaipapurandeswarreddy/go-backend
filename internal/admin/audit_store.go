package admin

import (
	"context"
	"time"

	"ambigo-backend/internal/ids"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditStore struct {
	pool *pgxpool.Pool
}

func NewAuditStore(pool *pgxpool.Pool) *AuditStore {
	return &AuditStore{pool: pool}
}

type AuditEvent struct {
	ID        string    `db:"id" json:"id"`
	EventType string    `db:"event_type" json:"event_type"`
	Channel   string    `db:"channel" json:"channel"`
	Payload   string    `db:"payload" json:"payload"`
	RequestID string    `db:"request_id" json:"request_id,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

func (s *AuditStore) InsertEvent(ctx context.Context, event *AuditEvent) error {
	if ids.IsZero(event.ID) {
		event.ID = ids.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	var requestID interface{}
	if event.RequestID == "" {
		requestID = nil
	} else {
		requestID = event.RequestID
	}
	const q = `INSERT INTO audit_log (id, event_type, channel, payload, request_id, created_at) VALUES ($1,$2,$3,$4::jsonb,$5,$6)`
	_, err := s.pool.Exec(ctx, q, event.ID, event.EventType, event.Channel, event.Payload, requestID, event.CreatedAt)
	return err
}

func (s *AuditStore) CleanupOldLogs(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM audit_log WHERE created_at < now() - interval '30 days'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
