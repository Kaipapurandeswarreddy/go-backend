package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/metrics"
	"ambigo-backend/internal/retry"

	"github.com/razorpay/razorpay-go"
	"github.com/sony/gobreaker"
)

type RazorpayService struct {
	client    *razorpay.Client
	KeyID     string
	KeySecret string
	breaker   *gobreaker.CircuitBreaker
}

func newRazorpayBreaker() *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "razorpay",
		MaxRequests: 20,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < 10 {
				return false
			}
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return failureRatio >= 0.5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Log.Info().Str("client", name).Str("from", from.String()).Str("to", to.String()).Msg("circuit breaker state changed")
			var val float64
			switch to {
			case gobreaker.StateClosed:
				val = 0
			case gobreaker.StateOpen:
				val = 1
			case gobreaker.StateHalfOpen:
				val = 2
			}
			metrics.CircuitBreakerState.WithLabelValues(name).Set(val)
			metrics.CircuitBreakerTransitions.WithLabelValues(name, from.String(), to.String()).Inc()
		},
	})
}

func NewRazorpayService(keyID, keySecret string) *RazorpayService {
	client := razorpay.NewClient(keyID, keySecret)
	return &RazorpayService{
		client:    client,
		KeyID:     keyID,
		KeySecret: keySecret,
		breaker:   newRazorpayBreaker(),
	}
}

// CreateOrder generates a new order ID from Razorpay for a given amount (in INR rupees)
func (s *RazorpayService) CreateOrder(amountINR float64, receipt string) (string, error) {
	if s.breaker != nil && s.breaker.State() == gobreaker.StateOpen {
		return "", fmt.Errorf("razorpay circuit breaker open: %w", gobreaker.ErrOpenState)
	}
	var orderID string
	_, err := s.breaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			// Razorpay expects amount in paise
			amountPaise := int(amountINR * 100)

			data := map[string]interface{}{
				"amount":   amountPaise,
				"currency": "INR",
				"receipt":  receipt,
			}

			body, err := s.client.Order.Create(data, nil)
			if err != nil {
				return err
			}

			id, ok := body["id"].(string)
			if !ok {
				return errors.New("invalid response from razorpay: missing order id")
			}

			orderID = id
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	return orderID, nil
}

// VerifySignature cryptographically validates the Razorpay callback
func (s *RazorpayService) VerifySignature(orderID, paymentID, signature string) bool {
	// Signature is HMAC SHA256 of "order_id|payment_id"
	data := orderID + "|" + paymentID

	h := hmac.New(sha256.New, []byte(s.KeySecret))
	h.Write([]byte(data))
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	return hmac.Equal([]byte(expectedSignature), []byte(signature))
}
