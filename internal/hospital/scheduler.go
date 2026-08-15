package hospital

import (
	"context"
	"time"

	"ambigo-backend/internal/logger"
)

// StartRefresh runs an initial seed shortly after boot, then re-evaluates all
// configured cities once per day (skipping those refreshed within MaxCacheAge).
// It is a no-op when no Google Places key is configured.
func (s *Seeder) StartRefresh() {
	if s.Places == nil || s.Places.APIKey == "" {
		logger.Log.Warn().Msg("Google Places API key is empty. Hospital seeding disabled.")
		return
	}

	go func() {
		time.AfterFunc(10*time.Second, s.refreshOnce)

		ticker := time.NewTicker(ResidentSeedInterval)
		defer ticker.Stop()
		for range ticker.C {
			s.refreshOnce()
		}
	}()
}

func (s *Seeder) refreshOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if n, err := s.SeedAll(ctx); err != nil {
		logger.Log.Error().Err(err).Msg("Hospital seed run failed")
	} else if n > 0 {
		logger.Log.Info().Int("changed", n).Msg("Hospital seed run completed")
	}
}
