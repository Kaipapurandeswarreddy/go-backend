package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"time"
)

// NonRetryableError wraps an error that should not be retried (4xx, validation, etc.).
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string { return e.Err.Error() }
func (e *NonRetryableError) Unwrap() error { return e.Err }

type Config struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

var Default = Config{
	MaxAttempts: 3,
	BaseDelay:   100 * time.Millisecond,
	MaxDelay:    2 * time.Second,
}

func Do(ctx context.Context, cfg Config, fn func(context.Context) error) error {
	var err error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		if err = fn(ctx); err == nil {
			return nil
		}
		// Do not retry non-retryable errors (4xx client errors) or context cancellation.
		var nre *NonRetryableError
		if errors.As(err, &nre) {
			return err
		}
		if strings.Contains(err.Error(), "status: 4") {
			// Covers 400, 401, 403, 404, 429 — all 4xx are client errors, not transient.
			// 429 should be handled by circuit breaker with Retry-After, not blind retry.
			return err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if attempt == cfg.MaxAttempts-1 {
			break
		}
		delay := time.Duration(math.Min(
			float64(cfg.BaseDelay)*math.Pow(2, float64(attempt)),
			float64(cfg.MaxDelay),
		))
		jitter := time.Duration(rand.Int63n(int64(delay / 2)))
		timer := time.NewTimer(delay + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
