package telephony

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/metrics"
	"ambigo-backend/internal/retry"

	"github.com/sony/gobreaker"
)

type CloudshopeService struct {
	Token       string
	CLINumber   string
	APIURL      string
	CountryCode string
	Client      *http.Client
	breaker     *gobreaker.CircuitBreaker
}

func newCloudshopeBreaker() *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "cloudshope",
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

func NewCloudshopeService(token, cliNumber, apiURL, countryCode string) *CloudshopeService {
	return &CloudshopeService{
		Token:       token,
		CLINumber:   cliNumber,
		APIURL:      apiURL,
		CountryCode: countryCode,
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
		breaker: newCloudshopeBreaker(),
	}
}

func (s *CloudshopeService) InitiateCallMasking(fromNumber, toNumber string) (string, error) {
	if s.breaker != nil && s.breaker.State() == gobreaker.StateOpen {
		return "", fmt.Errorf("cloudshope circuit breaker open: %w", gobreaker.ErrOpenState)
	}
	var result string
	_, err := s.breaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			url := s.APIURL

			payload := map[string]string{
				"from_number":   fromNumber,
				"mobile_number": toNumber,
				"cli_number":    s.CLINumber,
			}

			jsonBytes, err := json.Marshal(payload)
			if err != nil {
				return err
			}

			req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBytes))
			if err != nil {
				return err
			}

			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.Token))
			req.Header.Set("Content-Type", "application/json")

			resp, err := s.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				return errors.New(fmt.Sprintf("Cloudshope returned status: %d", resp.StatusCode))
			}

			// Returns the CLI Number in International format so the caller knows who to expect a call from
			result = fmt.Sprintf("+%s%s", s.CountryCode, s.CLINumber)
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	return result, nil
}
