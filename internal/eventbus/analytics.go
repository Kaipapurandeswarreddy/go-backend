package eventbus

import (
	"encoding/json"

	"ambigo-backend/internal/logger"
)

// AnalyticsTracker listens to ride and auth events for business analytics.
// Currently a stub — wire to your analytics backend (BigQuery, Kafka, etc.).
type AnalyticsTracker struct{}

func NewAnalyticsTracker() *AnalyticsTracker {
	return &AnalyticsTracker{}
}

func (a *AnalyticsTracker) SubscribeTo(bus *InMemoryBus) {
	bus.Subscribe(ChannelRideRequested, a.handleRideRequested)
	bus.Subscribe(ChannelRideCompleted, a.handleRideCompleted)
	bus.Subscribe(ChannelAuthUserRegistered, a.handleUserRegistered)
}

func (a *AnalyticsTracker) handleRideRequested(payload []byte) {
	var p RideRequestedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		logger.Log.Error().Err(err).Str("channel", "ride:requested").Msg("Unmarshal error")
		return
	}
	logger.Log.Info().Str("ride_id", p.RideID).Bool("sos", p.IsSOS).Str("mode", p.PaymentMode).Msg("Ride requested")
}

func (a *AnalyticsTracker) handleRideCompleted(payload []byte) {
	var p RideCompletedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		logger.Log.Error().Err(err).Str("channel", "ride:completed").Msg("Unmarshal error")
		return
	}
	logger.Log.Info().Str("ride_id", p.RideID).Float64("amount", p.FinalAmount).Str("mode", p.PaymentMode).Msg("Ride completed")
}

func (a *AnalyticsTracker) handleUserRegistered(payload []byte) {
	var p AuthUserRegisteredPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		logger.Log.Error().Err(err).Str("channel", "auth:user_registered").Msg("Unmarshal error")
		return
	}
	logger.Log.Info().Str("user_id", p.UserID).Msg("User registered")
}
