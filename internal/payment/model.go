package payment

import (
	"time"
)

type PaymentMode string

const (
	ModeCash   PaymentMode = "cash"
	ModeOnline PaymentMode = "online"
)

type Payment struct {
	ID                string       `db:"id" json:"_id"`
	UserID            string       `db:"user_id" json:"user_id"`
	PartnerID         string       `db:"partner_id" json:"partner_id"`
	RideID            string       `db:"ride_id" json:"ride_id"`
	Description       string       `db:"description" json:"description"`
	OriginalAmount    float64      `db:"original_amount" json:"original_amount"`
	ChargedAmount     float64      `db:"charged_amount" json:"charged_amount"`
	DriverShare       float64      `db:"driver_share" json:"driver_share"`
	PaymentMode       PaymentMode  `db:"payment_mode" json:"payment_mode"`
	Paid              bool         `db:"paid" json:"paid"`
	RazorpayOrderID   *string      `db:"razorpay_order_id" json:"razorpay_order_id,omitempty"`
	RazorpayPaymentID *string      `db:"razorpay_payment_id" json:"razorpay_payment_id,omitempty"`
	PaidAt            *time.Time   `db:"paid_at" json:"paid_at,omitempty"`
	CreatedAt         time.Time    `db:"created_at" json:"created_at"`
	Offer             *string      `db:"offer" json:"offer,omitempty"`
}
