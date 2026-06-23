package logger

import (
	"os"

	"github.com/rs/zerolog"
)

var Log zerolog.Logger

func init() {
	Log = zerolog.New(os.Stdout).With().Timestamp().Logger()
}

func NewRideLogger(rideID string) zerolog.Logger {
	return Log.With().Str("ride_id", rideID).Logger()
}

func NewUserLogger(userID string) zerolog.Logger {
	return Log.With().Str("user_id", userID).Logger()
}
