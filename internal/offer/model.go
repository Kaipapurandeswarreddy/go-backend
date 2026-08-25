package offer

type Offer struct {
	ID              string   `db:"id" json:"_id"`
	Description     string   `db:"description" json:"description" validate:"required"`
	UserID          *string  `db:"user_id" json:"user_id,omitempty"`
	OfferPercentage *float64 `db:"offer_percentage" json:"offer_percentage,omitempty"`
	OfferAmount     *float64 `db:"offer_amount" json:"offer_amount,omitempty"`
	MaxDiscount     *float64 `db:"max_discount" json:"max_discount,omitempty"`
}
