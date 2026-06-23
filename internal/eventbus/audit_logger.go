package eventbus

import (
	"ambigo-backend/internal/logger"
)

// AuditLogger listens to all events and writes them to the audit log.
type AuditLogger struct{}

func NewAuditLogger() *AuditLogger {
	return &AuditLogger{}
}

func (l *AuditLogger) SubscribeTo(bus *InMemoryBus) {
	channels := []string{
		ChannelRideRequested, ChannelRideDriverOffered,
		ChannelRideAccepted, ChannelRideArrived, ChannelRideStarted,
		ChannelRideCompleted, ChannelRideCancelled,
		ChannelAuthOTPRequested, ChannelAuthUserRegistered, ChannelAuthUserLoggedIn,
		ChannelAuthDriverCreated, ChannelAuthDriverLoggedIn, ChannelAuthDriverApproved,
		ChannelPaymentCompleted, ChannelWalletWithdrawal,
		ChannelDriverLocationUpdate,
		ChannelAdminAmbTypeCreated, ChannelAdminAmbTypeDeleted,
		ChannelAdminHospitalAdded, ChannelAdminHospitalUpdated, ChannelAdminHospitalDeleted,
	}
	for _, ch := range channels {
		bus.Subscribe(ch, l.handleEvent)
	}
}

func (l *AuditLogger) handleEvent(payload []byte) {
	logger.Log.Info().Str("channel", "audit").Str("payload", string(payload)).Msg("audit event")
}
