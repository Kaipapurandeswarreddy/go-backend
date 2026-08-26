package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"ambigo-backend/internal/auth"
	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/metrics"
	"ambigo-backend/internal/retry"

	"github.com/sony/gobreaker"
)

type ZwitchService struct {
	KeyID      string
	Secret     string
	AccountID  string
	APIBaseURL string
	Client     *http.Client
	breaker    *gobreaker.CircuitBreaker
}

func newZwitchBreaker() *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "zwitch",
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

func NewZwitchService(key, secret, accountID, apiBaseURL, proxyURL string) *ZwitchService {
	transport := &http.Transport{}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &ZwitchService{
		KeyID:      key,
		Secret:     secret,
		AccountID:  accountID,
		APIBaseURL: apiBaseURL,
		Client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
		breaker: newZwitchBreaker(),
	}
}

func (s *ZwitchService) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s:%s", s.KeyID, s.Secret))
	req.Header.Set("Content-Type", "application/json")
}

func (s *ZwitchService) VerifyBankAccount(acc *auth.WalletDetails, referenceID string) (string, error) {
	if s.breaker != nil && s.breaker.State() == gobreaker.StateOpen {
		return "", fmt.Errorf("zwitch circuit breaker open: %w", gobreaker.ErrOpenState)
	}
	var result string
	var resultErr error
	_, err := s.breaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			url := s.APIBaseURL + "/verifications/bank-account"
			payload := map[string]interface{}{
				"force_penny_drop":        false,
				"force_penny_drop_amount": 1,
				"bank_account_number":     acc.AccountNo,
				"bank_ifsc_code":          acc.IFSCCode,
				"merchant_reference_id":   referenceID,
			}

			body, _ := json.Marshal(payload)
			req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
			s.setHeaders(req)

			resp, err := s.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				return fmt.Errorf("zwitch verification failed status: %d", resp.StatusCode)
			}

			var data map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&data)
			if status, ok := data["status"].(string); ok {
				result = status
				return nil
			}
			return errors.New("missing status")
		})
	})
	if err != nil {
		return "", err
	}
	if resultErr != nil {
		return "", resultErr
	}
	return result, nil
}

func (s *ZwitchService) CreateBeneficiary(acc *auth.WalletDetails, driverID string) (string, error) {
	if s.breaker != nil && s.breaker.State() == gobreaker.StateOpen {
		return "", fmt.Errorf("zwitch circuit breaker open: %w", gobreaker.ErrOpenState)
	}
	var result string
	_, err := s.breaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			url := fmt.Sprintf("%s/accounts/%s/beneficiaries", s.APIBaseURL, s.AccountID)
			payload := map[string]interface{}{
				"type":                   "account_number",
				"name_of_account_holder": acc.BenfName,
				"bank_account_number":    acc.AccountNo,
				"bank_ifsc_code":         acc.IFSCCode,
				"metadata": map[string]string{
					"driver_uid": driverID,
				},
			}

			body, _ := json.Marshal(payload)
			req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
			s.setHeaders(req)

			resp, err := s.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				respBody, _ := io.ReadAll(resp.Body)
				logger.Log.Error().Int("status", resp.StatusCode).Str("body", string(respBody)).Msg("Zwitch create beneficiary failed")
				return fmt.Errorf("zwitch create beneficiary failed status: %d - %s", resp.StatusCode, string(respBody))
			}

			var data map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&data)
			if id, ok := data["id"].(string); ok {
				result = id
				return nil
			}
			return errors.New("missing id")
		})
	})
	if err != nil {
		return "", err
	}
	return result, nil
}

func (s *ZwitchService) UpdateBeneficiaryName(acc *auth.WalletDetails) error {
	if s.breaker != nil && s.breaker.State() == gobreaker.StateOpen {
		return fmt.Errorf("zwitch circuit breaker open: %w", gobreaker.ErrOpenState)
	}
	_, err := s.breaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			url := fmt.Sprintf("%s/accounts/beneficiaries/%s", s.APIBaseURL, acc.BenfID)
			payload := map[string]interface{}{
				"name_of_account_holder": acc.BenfName,
			}

			body, _ := json.Marshal(payload)
			req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
			s.setHeaders(req)

			resp, err := s.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				return fmt.Errorf("zwitch update beneficiary failed status: %d", resp.StatusCode)
			}

			return nil
		})
	})
	return err
}

func (s *ZwitchService) DeleteBeneficiary(benfID string) error {
	if s.breaker != nil && s.breaker.State() == gobreaker.StateOpen {
		return fmt.Errorf("zwitch circuit breaker open: %w", gobreaker.ErrOpenState)
	}
	_, err := s.breaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			url := fmt.Sprintf("%s/accounts/beneficiaries/%s", s.APIBaseURL, benfID)
			req, _ := http.NewRequestWithContext(ctx, "DELETE", url, nil)
			s.setHeaders(req)

			resp, err := s.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				return fmt.Errorf("zwitch delete beneficiary failed status: %d", resp.StatusCode)
			}
			return nil
		})
	})
	return err
}

func (s *ZwitchService) CreateTransfer(acc *auth.WalletDetails, amount float64, referenceID string) (map[string]interface{}, error) {
	if s.breaker != nil && s.breaker.State() == gobreaker.StateOpen {
		return nil, fmt.Errorf("zwitch circuit breaker open: %w", gobreaker.ErrOpenState)
	}
	var result map[string]interface{}
	_, err := s.breaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			url := s.APIBaseURL + "/transfers"
			payload := map[string]interface{}{
				"type":                  "account_number",
				"currency_code":         "inr",
				"debit_account_id":      s.AccountID,
				"beneficiary_id":        acc.BenfID,
				"amount":                amount,
				"payment_mode":          "neft",
				"merchant_reference_id": referenceID,
				"async":                 false,
			}

			body, _ := json.Marshal(payload)
			req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
			s.setHeaders(req)

			resp, err := s.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			var data map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&data)

			if resp.StatusCode >= 400 {
				result = data
				return fmt.Errorf("zwitch transfer failed status: %d", resp.StatusCode)
			}

			result = data
			return nil
		})
	})
	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) {
			return nil, err
		}
		// result may already be set on 4xx case
		if result != nil {
			return result, err
		}
		return nil, err
	}
	return result, nil
}
