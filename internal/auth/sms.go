package auth

import (
	"bytes"
	"context"
	"encoding/base64"
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

var smsClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:    5,
		IdleConnTimeout: 90 * time.Second,
	},
}

var smsBreaker *gobreaker.CircuitBreaker

func init() {
	smsBreaker = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "sms",
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
	go smsWorker()
}

// smsJob represents an async SMS delivery task.
type smsJob struct {
	cfg          SMSCountryConfig
	number       string
	otp          string
	appSignature string
}

// smsJobs is the buffered queue for async SMS delivery.
// 512 capacity prevents Publish/HTTP handlers from blocking on SMS provider latency.
var smsJobs = make(chan smsJob, 512)

func smsWorker() {
	for job := range smsJobs {
		if err := SendSMS(job.cfg, job.number, job.otp, job.appSignature); err != nil {
			// If circuit breaker is open, return error immediately and let caller enqueue to async channel (already async)
			if errors.Is(err, gobreaker.ErrOpenState) {
				logger.Log.Warn().Str("mobile", job.number).Msg("SMS circuit breaker open, skipping retry")
				continue
			}
			logger.Log.Warn().Err(err).Str("mobile", job.number).Msg("SMS send failed, retrying")
			time.Sleep(2 * time.Second)
			if err2 := SendSMS(job.cfg, job.number, job.otp, job.appSignature); err2 != nil {
				logger.Log.Error().Err(err2).Str("mobile", job.number).Msg("SMS send failed after retry")
			} else {
				logger.Log.Info().Str("mobile", job.number).Msg("SMS sent on retry")
			}
		}
	}
}

// SendSMSAsync enqueues an SMS for async delivery and returns immediately.
// The OTP has already been persisted before this call, so callers can respond
// 200 without waiting for the 10s provider round-trip.
func SendSMSAsync(cfg SMSCountryConfig, number string, otp string, appSignature string) {
	job := smsJob{cfg: cfg, number: number, otp: otp, appSignature: appSignature}
	select {
	case smsJobs <- job:
	default:
		logger.Log.Warn().Str("mobile", number).Msg("SMS queue full, dispatching in dedicated goroutine")
		go func(j smsJob) {
			if err := SendSMS(j.cfg, j.number, j.otp, j.appSignature); err != nil {
				if errors.Is(err, gobreaker.ErrOpenState) {
					logger.Log.Warn().Str("mobile", j.number).Msg("SMS circuit breaker open, async fallback skipped")
					return
				}
				logger.Log.Error().Err(err).Str("mobile", j.number).Msg("SMS async fallback failed")
				time.Sleep(2 * time.Second)
				if err2 := SendSMS(j.cfg, j.number, j.otp, j.appSignature); err2 != nil {
					logger.Log.Error().Err(err2).Str("mobile", j.number).Msg("SMS async fallback retry failed")
				}
			}
		}(job)
	}
}

// SMSCountryConfig holds configuration for SMSCountry API
type SMSCountryConfig struct {
	APIKey     string
	APIToken   string
	APIBaseURL string
	SenderID   string
	CC         string // country code prefix
}

// SendSMS calls the SMSCountry API to send an OTP
func SendSMS(cfg SMSCountryConfig, number string, otp string, appSignature string) error {
	if cfg.APIKey == "" || cfg.APIToken == "" {
		return fmt.Errorf("SMS_COUNTRY_KEY or SMS_COUNTRY_TOKEN is not set in environment")
	}

	// On open, return error immediately and let caller enqueue to async channel (already async)
	if smsBreaker != nil && smsBreaker.State() == gobreaker.StateOpen {
		return fmt.Errorf("sms circuit breaker open: %w", gobreaker.ErrOpenState)
	}

	_, err := smsBreaker.Execute(func() (interface{}, error) {
		return nil, retry.Do(context.Background(), retry.Default, func(ctx context.Context) error {
			credentials := fmt.Sprintf("%s:%s", cfg.APIKey, cfg.APIToken)
			encodedCredentials := base64.StdEncoding.EncodeToString([]byte(credentials))
			url := fmt.Sprintf(cfg.APIBaseURL, cfg.APIKey)

			// Build the message exactly like V1
			msgContent := fmt.Sprintf("Your Ambigo verification code is: %s. Please do not share it with anyone else.", otp)
			if appSignature != "" {
				msgContent += "\n\n" + appSignature
			}

			payload := map[string]string{
				"Number":   cfg.CC + number,
				"Text":     msgContent,
				"SenderId": cfg.SenderID,
			}

			jsonPayload, err := json.Marshal(payload)
			if err != nil {
				return err
			}

			req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
			if err != nil {
				return err
			}

			req.Header.Set("Authorization", "Basic "+encodedCredentials)
			req.Header.Set("Content-Type", "application/json")

			resp, err := smsClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				return fmt.Errorf("SMS provider returned status code %d", resp.StatusCode)
			}

			return nil
		})
	})
	if err != nil {
		return err
	}
	return nil
}
