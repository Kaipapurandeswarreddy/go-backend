package response

import (
	"encoding/json"
	"net/http"

	"ambigo-backend/internal/logger"
)

func Error(w http.ResponseWriter, message string, code int) {
	logger.Log.Warn().Int("code", code).Str("detail", message).Msg("API error response")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   http.StatusText(code),
		"detail":  message,
		"code":    code,
	})
}

// ErrorWithReason is like Error but adds a machine-readable reason so clients
// can distinguish failure causes (e.g. "session_replaced" vs generic expiry).
func ErrorWithReason(w http.ResponseWriter, message, reason string, code int) {
	logger.Log.Warn().Int("code", code).Str("reason", reason).Str("detail", message).Msg("API error response")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   http.StatusText(code),
		"detail":  message,
		"reason":  reason,
		"code":    code,
	})
}

func Success(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}
