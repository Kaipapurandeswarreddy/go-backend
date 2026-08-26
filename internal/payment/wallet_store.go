package payment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"ambigo-backend/internal/ids"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WalletTransaction mirrors the Records.wallet collection, now stored in
// wallet_transactions table per migrations/00001_init.sql.
type WalletTransaction struct {
	ID                  string     `db:"id" json:"_id"`
	DriverID            string     `db:"driver_id" json:"driver_id"`
	ZwitchBeneficiaryID string     `db:"zwitch_beneficiary_id" json:"zwitch_beneficiary_id"`
	ZwitchID            string     `db:"zwitch_id" json:"zwitch_id"`
	Amount              float64    `db:"amount" json:"amount"`
	AccountNo           string     `db:"account_no" json:"account_no"`
	MerchantReferenceID string     `db:"merchant_reference_id" json:"merchant_reference_id"`
	BankReferenceNo     string     `db:"bank_reference_no" json:"bank_reference_no"`
	ZwitchTransferID    string     `db:"zwitch_transfer_id" json:"zwitch_transfer_id"`
	Status              string     `db:"status" json:"status"`
	ErrorMessage        string     `db:"error_message" json:"error_message"`
	CreatedAt           time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt           *time.Time `db:"updated_at" json:"updated_at,omitempty"`
}

// WalletStore handles wallet_transactions and drivers.wallet_balance /
// drivers.wallet_details. In Postgres both tables live in the same DB
// (single pool), but the store is usable inside a pgx.Tx via DBTX.
type WalletStore struct {
	pool *pgxpool.Pool
	db   DBTX
}

// NewWalletStore creates a WalletStore backed by a pgxpool.Pool.
// The original Mongo version required two databases (Records + Users); the
// Postgres version uses a single pool since both tables are in the public
// schema (wallet_transactions + drivers).
func NewWalletStore(pool *pgxpool.Pool) *WalletStore {
	return &WalletStore{pool: pool, db: pool}
}

// NewWalletStoreWithDB creates a WalletStore from any DBTX (pool or Tx).
func NewWalletStoreWithDB(db DBTX) *WalletStore {
	return &WalletStore{db: db}
}

// WithTx returns a new WalletStore bound to the given transaction.
func (s *WalletStore) WithTx(tx pgx.Tx) *WalletStore {
	return &WalletStore{pool: s.pool, db: tx}
}

// Pool returns the underlying pool (may be nil if created via NewWalletStoreWithDB).
func (s *WalletStore) Pool() *pgxpool.Pool { return s.pool }

const walletSelect = `SELECT id::text, driver_id, zwitch_beneficiary_id, zwitch_id, amount, account_no, merchant_reference_id, bank_reference_no, zwitch_transfer_id, status, error_message, created_at, updated_at FROM wallet_transactions`

func scanWalletTransaction(row pgx.Row) (*WalletTransaction, error) {
	var w WalletTransaction
	err := row.Scan(
		&w.ID,
		&w.DriverID,
		&w.ZwitchBeneficiaryID,
		&w.ZwitchID,
		&w.Amount,
		&w.AccountNo,
		&w.MerchantReferenceID,
		&w.BankReferenceNo,
		&w.ZwitchTransferID,
		&w.Status,
		&w.ErrorMessage,
		&w.CreatedAt,
		&w.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// InsertTransaction inserts a new wallet transaction. ID and CreatedAt are
// generated if empty/zero via ids.New() / time.Now().
func (s *WalletStore) InsertTransaction(ctx context.Context, tx *WalletTransaction) error {
	if tx.ID == "" {
		tx.ID = ids.New()
	}
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO wallet_transactions (id, driver_id, zwitch_beneficiary_id, zwitch_id, amount, account_no, merchant_reference_id, bank_reference_no, zwitch_transfer_id, status, error_message, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		tx.ID, tx.DriverID, tx.ZwitchBeneficiaryID, tx.ZwitchID, tx.Amount, tx.AccountNo, tx.MerchantReferenceID, tx.BankReferenceNo, tx.ZwitchTransferID, tx.Status, tx.ErrorMessage, tx.CreatedAt, tx.UpdatedAt,
	)
	return err
}

// ListTransactions returns the most recent 50 wallet transactions for a driver, ordered by
// created_at descending. Use ListTransactionsPaginated for keyset pagination.
func (s *WalletStore) ListTransactions(ctx context.Context, driverID string) ([]WalletTransaction, error) {
	return s.ListTransactionsPaginated(ctx, driverID, 50, "")
}

// ListTransactionsPaginated returns wallet transactions with keyset pagination (capped at 50).
func (s *WalletStore) ListTransactionsPaginated(ctx context.Context, driverID string, limit int, cursor string) ([]WalletTransaction, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	var rows pgx.Rows
	var err error
	if cursor != "" && ids.IsValid(cursor) {
		// cursor is an id; keyset on (created_at, id)
		rows, err = s.db.Query(ctx, walletSelect+` WHERE driver_id=$1 AND (created_at, id) < ((SELECT created_at FROM wallet_transactions WHERE id=$3::uuid), $3::uuid) ORDER BY created_at DESC, id DESC LIMIT $2`, driverID, limit, cursor)
	} else {
		rows, err = s.db.Query(ctx, walletSelect+` WHERE driver_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`, driverID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []WalletTransaction
	for rows.Next() {
		var w WalletTransaction
		if err := rows.Scan(
			&w.ID,
			&w.DriverID,
			&w.ZwitchBeneficiaryID,
			&w.ZwitchID,
			&w.Amount,
			&w.AccountNo,
			&w.MerchantReferenceID,
			&w.BankReferenceNo,
			&w.ZwitchTransferID,
			&w.Status,
			&w.ErrorMessage,
			&w.CreatedAt,
			&w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []WalletTransaction{}
	}
	return list, nil
}

func (s *WalletStore) ListTransactionsWithOffset(ctx context.Context, driverID string, limit int, offset int) ([]WalletTransaction, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, walletSelect+` WHERE driver_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, driverID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []WalletTransaction
	for rows.Next() {
		var w WalletTransaction
		if err := rows.Scan(
			&w.ID,
			&w.DriverID,
			&w.ZwitchBeneficiaryID,
			&w.ZwitchID,
			&w.Amount,
			&w.AccountNo,
			&w.MerchantReferenceID,
			&w.BankReferenceNo,
			&w.ZwitchTransferID,
			&w.Status,
			&w.ErrorMessage,
			&w.CreatedAt,
			&w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []WalletTransaction{}
	}
	return list, nil
}

// UpdateWalletBalance increments or decrements the driver's wallet balance.
// A negative amount indicates a debit. Postgres equivalent of Mongo
// `$inc: {wallet_balance: amount}` is:
// `UPDATE drivers SET wallet_balance = wallet_balance + $2 WHERE id=$1`
func (s *WalletStore) UpdateWalletBalance(ctx context.Context, driverID string, amount float64) error {
	_, err := s.db.Exec(ctx,
		`UPDATE drivers SET wallet_balance = wallet_balance + $2 WHERE id=$1`,
		driverID, amount,
	)
	return err
}

// DeductBalance atomically deducts amount only if sufficient balance exists.
// Prevents concurrent withdrawals from driving balance negative.
// Postgres: `UPDATE drivers SET wallet_balance = wallet_balance - $2 WHERE id=$1 AND wallet_balance >= $2`
// Check RowsAffected==0 -> insufficient wallet balance. This is the atomic
// CAS guard that replaces Mongo's `{"wallet_balance": {"$gte": amount}}` + `$inc`.
func (s *WalletStore) DeductBalance(ctx context.Context, driverID string, amount float64) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE drivers SET wallet_balance = wallet_balance - $2 WHERE id=$1 AND wallet_balance >= $2`,
		driverID, amount,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("insufficient wallet balance")
	}
	return nil
}

// UpdateWalletDetails saves the driver's bank / Zwitch details into
// drivers.wallet_details (JSONB). The original Mongo used `$set: {wallet_details: details}`.
// Postgres: `UPDATE drivers SET wallet_details=$2 WHERE id=$1`.
// details is marshalled to JSON; WalletDetails struct works directly.
func (s *WalletStore) UpdateWalletDetails(ctx context.Context, driverID string, details interface{}) error {
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`UPDATE drivers SET wallet_details=$2::jsonb, updated_at=now() WHERE id=$1::uuid`,
		driverID, data,
	)
	return err
}

// UpdateTransactionStatus updates the status and auxiliary fields of a wallet
// transaction identified by merchant_reference_id. Used in the withdrawal Saga
// (Tx2 after Zwitch call and compensating refund Tx2b) per
// docs/migration/03-tech-choices-evaluation.md §6.
func (s *WalletStore) UpdateTransactionStatus(ctx context.Context, merchantReferenceID string, status string, bankReferenceNo string, zwitchTransferID string, errorMessage string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE wallet_transactions SET status=$2, bank_reference_no=$3, zwitch_transfer_id=$4, error_message=$5, updated_at=now() WHERE merchant_reference_id=$1`,
		merchantReferenceID, status, bankReferenceNo, zwitchTransferID, errorMessage,
	)
	return err
}
