package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"

	"ambigo-backend/api/response"
	"ambigo-backend/internal/auth"
)

type AttendantAuthHandler struct {
	AuthStore *auth.Store
	JWTSecret string
	SMSCfg    auth.SMSCountryConfig
}

func NewAttendantAuthHandler(authStore *auth.Store, jwtSecret string, smsCfg auth.SMSCountryConfig) *AttendantAuthHandler {
	return &AttendantAuthHandler{AuthStore: authStore, JWTSecret: jwtSecret, SMSCfg: smsCfg}
}

var attendantMobileRegex = regexp.MustCompile(`^[6-9]\d{9}$`)

func (h *AttendantAuthHandler) HandleAttendantRequestOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mobile       string `json:"mobile"`
		AppSignature string `json:"app_signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if !attendantMobileRegex.MatchString(req.Mobile) {
		response.Error(w, "Invalid mobile number", http.StatusBadRequest)
		return
	}
	locked, _ := h.AuthStore.IsOTPLocked(r.Context(), req.Mobile)
	if locked {
		response.Error(w, "Too many attempts. Try again later.", http.StatusTooManyRequests)
		return
	}
	otp, err := h.AuthStore.GenerateAndStoreOTP(r.Context(), req.Mobile)
	if err != nil {
		response.Error(w, "Failed to generate OTP", http.StatusInternalServerError)
		return
	}
	_ = auth.SendSMS(h.SMSCfg, req.Mobile, otp, req.AppSignature)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"detail": "OTP sent"})
}

func (h *AttendantAuthHandler) HandleAttendantVerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mobile     string `json:"mobile"`
		OTP        string `json:"otp"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		Name       string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if !attendantMobileRegex.MatchString(req.Mobile) || req.OTP == "" {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	locked, _ := h.AuthStore.IsOTPLocked(r.Context(), req.Mobile)
	if locked {
		response.Error(w, "Too many attempts", http.StatusTooManyRequests)
		return
	}
	ok, _ := h.AuthStore.VerifyOTP(r.Context(), req.Mobile, req.OTP)
	if !ok {
		_ = h.AuthStore.IncrementFailedOTP(r.Context(), req.Mobile)
		response.Error(w, "Invalid or expired OTP", http.StatusUnauthorized)
		return
	}
	_ = h.AuthStore.ResetFailedOTP(r.Context(), req.Mobile)

	att, err := h.AuthStore.FindAmbulanceAttendantByMobile(r.Context(), req.Mobile)
	if err != nil {
		response.Error(w, "Failed to lookup attendant", http.StatusInternalServerError)
		return
	}
	if att == nil {
		if req.Name == "" {
			req.Name = "Attendant"
		}
		att = &auth.AmbulanceAttendant{Name: req.Name, Mobile: req.Mobile}
		if err := h.AuthStore.CreateAmbulanceAttendant(r.Context(), att); err != nil {
			response.Error(w, "Failed to create attendant", http.StatusInternalServerError)
			return
		}
	}
	// Single session like driver
	_, _ = h.AuthStore.RevokeAllUserRefreshTokens(r.Context(), att.ID.Hex(), "session_replaced")
	accessToken, err := auth.GenerateAccessToken(att.ID.Hex(), "attendant", h.JWTSecret)
	if err != nil {
		response.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}
	sessionID := auth.NewSessionID()
	refreshStr, _, err := h.AuthStore.CreateRefreshToken(r.Context(), att.ID.Hex(), "attendant", sessionID, req.DeviceID, req.DeviceName)
	if err != nil {
		response.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	_ = h.AuthStore.UpdateAmbulanceAttendantJWT(r.Context(), att.ID, accessToken)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": refreshStr,
		"session_id":    sessionID,
		"role":          "attendant",
	})
}
