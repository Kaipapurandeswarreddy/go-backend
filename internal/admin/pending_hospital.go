package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ambigo-backend/internal/ids"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PendingHospital struct {
	ID              string     `db:"id" json:"_id"`
	Name            string     `db:"name" json:"name"`
	Address         string     `db:"address" json:"address"`
	Email           string     `db:"email" json:"email"`
	MDNumber        string     `db:"md_number" json:"md_number"`
	OfficialNumber  string     `db:"official_number" json:"official_number"`
	City            string     `db:"city" json:"city"`
	Location        *GeoJSON   `db:"location" json:"location,omitempty"`
	Status          string     `db:"status" json:"status"`
	RejectionReason *string    `db:"rejection_reason" json:"rejection_reason,omitempty"`
	MDID            string     `db:"md_id" json:"md_id,omitempty"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	ReviewedAt      *time.Time `db:"reviewed_at" json:"reviewed_at,omitempty"`
	ReviewedBy      *string    `db:"reviewed_by" json:"reviewed_by,omitempty"`
}

type PendingHospitalStore struct {
	pool *pgxpool.Pool
}

func NewPendingHospitalStore(pool *pgxpool.Pool) *PendingHospitalStore {
	return &PendingHospitalStore{pool: pool}
}

const pendingHospitalColumns = `id, name, address, email, md_number, official_number, city, location, status, rejection_reason, md_id, created_at, reviewed_at, reviewed_by`

func scanPendingHospital(row pgx.Row) (*PendingHospital, error) {
	var p PendingHospital
	var (
		locationBytes   []byte
		rejectionReason sql.NullString
		reviewedAt      sql.NullTime
		reviewedBy      sql.NullString
		mdID            sql.NullString
	)
	err := row.Scan(
		&p.ID,
		&p.Name,
		&p.Address,
		&p.Email,
		&p.MDNumber,
		&p.OfficialNumber,
		&p.City,
		&locationBytes,
		&p.Status,
		&rejectionReason,
		&mdID,
		&p.CreatedAt,
		&reviewedAt,
		&reviewedBy,
	)
	if err != nil {
		return nil, err
	}
	if len(locationBytes) > 0 && string(locationBytes) != "null" {
		var loc GeoJSON
		if err := json.Unmarshal(locationBytes, &loc); err != nil {
			return nil, fmt.Errorf("unmarshal pending_hospital location: %w", err)
		}
		p.Location = &loc
	}
	if rejectionReason.Valid {
		p.RejectionReason = &rejectionReason.String
	}
	if mdID.Valid {
		p.MDID = mdID.String
	}
	if reviewedAt.Valid {
		p.ReviewedAt = &reviewedAt.Time
	}
	if reviewedBy.Valid {
		p.ReviewedBy = &reviewedBy.String
	}
	return &p, nil
}

func (s *PendingHospitalStore) Create(ctx context.Context, p *PendingHospital) error {
	if ids.IsZero(p.ID) {
		p.ID = ids.New()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if p.Status == "" {
		p.Status = "pending"
	}
	var locationJSON []byte
	var err error
	if p.Location != nil {
		locationJSON, err = json.Marshal(p.Location)
		if err != nil {
			return fmt.Errorf("marshal pending_hospital location: %w", err)
		}
	}
	var rejectionReason interface{}
	if p.RejectionReason != nil {
		rejectionReason = *p.RejectionReason
	} else {
		rejectionReason = nil
	}
	var mdID interface{}
	if p.MDID != "" {
		mdID = p.MDID
	} else {
		mdID = nil
	}
	var reviewedAt interface{}
	if p.ReviewedAt != nil {
		reviewedAt = *p.ReviewedAt
	} else {
		reviewedAt = nil
	}
	var reviewedBy interface{}
	if p.ReviewedBy != nil {
		reviewedBy = *p.ReviewedBy
	} else {
		reviewedBy = nil
	}
	const q = `INSERT INTO pending_hospitals (id, name, address, email, md_number, official_number, city, location, status, rejection_reason, md_id, created_at, reviewed_at, reviewed_by)
	           VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14)`
	_, err = s.pool.Exec(ctx, q,
		p.ID,
		p.Name,
		p.Address,
		p.Email,
		p.MDNumber,
		p.OfficialNumber,
		p.City,
		locationJSON,
		p.Status,
		rejectionReason,
		mdID,
		p.CreatedAt,
		reviewedAt,
		reviewedBy,
	)
	return err
}

func (s *PendingHospitalStore) FindByID(ctx context.Context, id string) (*PendingHospital, error) {
	if !ids.IsValid(id) {
		return nil, fmt.Errorf("invalid pending_hospital id: %s", id)
	}
	const q = `SELECT ` + pendingHospitalColumns + ` FROM pending_hospitals WHERE id=$1::uuid`
	p, err := scanPendingHospital(s.pool.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

func (s *PendingHospitalStore) FindByMDNumber(ctx context.Context, mdNumber string) (*PendingHospital, error) {
	const q = `SELECT ` + pendingHospitalColumns + ` FROM pending_hospitals WHERE md_number=$1 AND status='pending' LIMIT 1`
	p, err := scanPendingHospital(s.pool.QueryRow(ctx, q, mdNumber))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

func (s *PendingHospitalStore) ListPending(ctx context.Context) ([]PendingHospital, error) {
	const q = `SELECT ` + pendingHospitalColumns + ` FROM pending_hospitals WHERE status='pending' ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []PendingHospital
	for rows.Next() {
		p, err := scanPendingHospital(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []PendingHospital{}
	}
	return list, nil
}

func (s *PendingHospitalStore) ListAll(ctx context.Context) ([]PendingHospital, error) {
	const q = `SELECT ` + pendingHospitalColumns + ` FROM pending_hospitals ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []PendingHospital
	for rows.Next() {
		p, err := scanPendingHospital(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []PendingHospital{}
	}
	return list, nil
}

func (s *PendingHospitalStore) Approve(ctx context.Context, id string, reviewerID string) error {
	if !ids.IsValid(id) {
		return fmt.Errorf("invalid pending_hospital id: %s", id)
	}
	const q = `UPDATE pending_hospitals SET status='approved', reviewed_at=now(), reviewed_by=$2 WHERE id=$1::uuid AND status='pending'`
	_, err := s.pool.Exec(ctx, q, id, reviewerID)
	return err
}

func (s *PendingHospitalStore) Reject(ctx context.Context, id string, reviewerID, reason string) error {
	if !ids.IsValid(id) {
		return fmt.Errorf("invalid pending_hospital id: %s", id)
	}
	const q = `UPDATE pending_hospitals SET status='rejected', rejection_reason=$3, reviewed_at=now(), reviewed_by=$2 WHERE id=$1::uuid AND status='pending'`
	_, err := s.pool.Exec(ctx, q, id, reviewerID, reason)
	return err
}

func (s *PendingHospitalStore) Delete(ctx context.Context, id string) error {
	if !ids.IsValid(id) {
		return fmt.Errorf("invalid pending_hospital id: %s", id)
	}
	const q = `DELETE FROM pending_hospitals WHERE id=$1::uuid`
	_, err := s.pool.Exec(ctx, q, id)
	return err
}

func (s *PendingHospitalStore) CountPending(ctx context.Context) (int64, error) {
	const q = `SELECT COUNT(*) FROM pending_hospitals WHERE status='pending'`
	var count int64
	err := s.pool.QueryRow(ctx, q).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}
