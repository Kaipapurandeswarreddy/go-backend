package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"

	"ambigo-backend/api/middleware"
	"ambigo-backend/api/response"
	"ambigo-backend/internal/auth"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/bcrypt"
)

type HospitalReceptionistHandler struct {
	AuthStore *auth.Store
	JWTSecret string
}

func NewHospitalReceptionistHandler(authStore *auth.Store, jwtSecret string) *HospitalReceptionistHandler {
	return &HospitalReceptionistHandler{AuthStore: authStore, JWTSecret: jwtSecret}
}

var receptionistUsernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{4,20}$`)

func (h *HospitalReceptionistHandler) HandleCreateReceptionist(w http.ResponseWriter, r *http.Request) {
	mdIDStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "hospital_md" {
		response.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	mdID, _ := primitive.ObjectIDFromHex(mdIDStr)
	md, err := h.AuthStore.FindHospitalMDByID(r.Context(), mdID)
	if err != nil || md == nil || md.HospitalID == nil {
		response.Error(w, "MD not linked to hospital", http.StatusBadRequest)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Mobile   string `json:"mobile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if !receptionistUsernameRegex.MatchString(req.Username) {
		response.Error(w, "Username must be 4-20 alphanumeric/underscore", http.StatusBadRequest)
		return
	}
	if len(req.Password) < 8 {
		response.Error(w, "Password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		response.Error(w, "Name required", http.StatusBadRequest)
		return
	}
	existing, _ := h.AuthStore.FindHospitalReceptionistByUsername(r.Context(), req.Username)
	if existing != nil {
		response.Error(w, "Username already taken", http.StatusBadRequest)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		response.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}
	hashStr := string(hash)
	var mobilePtr *string
	if req.Mobile != "" {
		mobilePtr = &req.Mobile
	}
	recept := &auth.HospitalReceptionist{
		HospitalID:    *md.HospitalID,
		CreatedByMDID: md.ID,
		Name:          req.Name,
		Username:      req.Username,
		PasswordHash:  hashStr,
		Mobile:        mobilePtr,
		Active:        true,
	}
	if err := h.AuthStore.CreateHospitalReceptionist(r.Context(), recept); err != nil {
		response.Error(w, "Failed to create receptionist", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"detail": "Receptionist created", "id": recept.ID.Hex()})
}

func (h *HospitalReceptionistHandler) HandleListReceptionists(w http.ResponseWriter, r *http.Request) {
	mdIDStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mdID, _ := primitive.ObjectIDFromHex(mdIDStr)
	md, err := h.AuthStore.FindHospitalMDByID(r.Context(), mdID)
	if err != nil || md == nil || md.HospitalID == nil {
		response.Error(w, "MD not linked to hospital", http.StatusBadRequest)
		return
	}
	list, err := h.AuthStore.ListReceptionistsByHospital(r.Context(), *md.HospitalID)
	if err != nil {
		response.Error(w, "Failed to fetch", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *HospitalReceptionistHandler) HandleDeleteReceptionist(w http.ResponseWriter, r *http.Request) {
	mdIDStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mdID, _ := primitive.ObjectIDFromHex(mdIDStr)
	md, err := h.AuthStore.FindHospitalMDByID(r.Context(), mdID)
	if err != nil || md == nil || md.HospitalID == nil {
		response.Error(w, "MD not linked to hospital", http.StatusBadRequest)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		response.Error(w, "ID required", http.StatusBadRequest)
		return
	}
	objID, err := primitive.ObjectIDFromHex(req.ID)
	if err != nil {
		response.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}
	recept, err := h.AuthStore.FindHospitalReceptionistByID(r.Context(), objID)
	if err != nil || recept == nil {
		response.Error(w, "Receptionist not found", http.StatusNotFound)
		return
	}
	if recept.HospitalID != *md.HospitalID {
		response.Error(w, "Forbidden: different hospital", http.StatusForbidden)
		return
	}
	if err := h.AuthStore.DeleteHospitalReceptionist(r.Context(), objID); err != nil {
		response.Error(w, "Delete failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"detail": "Receptionist deleted"})
}

func (h *HospitalReceptionistHandler) HandleReceptionistLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	recept, err := h.AuthStore.FindHospitalReceptionistByUsername(r.Context(), req.Username)
	if err != nil || recept == nil {
		response.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}
	if !recept.Active {
		response.Error(w, "Account disabled", http.StatusForbidden)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(recept.PasswordHash), []byte(req.Password)) != nil {
		response.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}
	accessToken, err := auth.GenerateHospitalJWT(recept.ID.Hex(), "hospital_receptionist", recept.HospitalID.Hex(), h.JWTSecret)
	if err != nil {
		response.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}
	sessionID := auth.NewSessionID()
	refreshStr, _, err := h.AuthStore.CreateRefreshToken(r.Context(), recept.ID.Hex(), "hospital_receptionist", sessionID, req.DeviceID, req.DeviceName)
	if err != nil {
		response.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	_ = h.AuthStore.UpdateHospitalReceptionistJWT(r.Context(), recept.ID, accessToken)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": refreshStr,
		"session_id":    sessionID,
		"role":          "hospital_receptionist",
		"hospital_id":   recept.HospitalID.Hex(),
	})
}

func (h *HospitalReceptionistHandler) HandleReceptionistMe(w http.ResponseWriter, r *http.Request) {
	uid, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "hospital_receptionist" && role != "hospital_md" {
		response.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// Try receptionist first
	if role == "hospital_receptionist" {
		oid, _ := primitive.ObjectIDFromHex(uid)
		recept, err := h.AuthStore.FindHospitalReceptionistByID(r.Context(), oid)
		if err == nil && recept != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          recept.ID.Hex(),
				"username":    recept.Username,
				"name":        recept.Name,
				"hospital_id": recept.HospitalID.Hex(),
				"role":        "hospital_receptionist",
			})
			return
		}
	}
	// Fallback: hospital_md also can call me
	oid, _ := primitive.ObjectIDFromHex(uid)
	md, err := h.AuthStore.FindHospitalMDByID(r.Context(), oid)
	if err == nil && md != nil {
		var hid interface{}
		if md.HospitalID != nil {
			hid = md.HospitalID.Hex()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":          md.ID.Hex(),
			"username":    md.Username,
			"name":        md.Name,
			"hospital_id": hid,
			"role":        "hospital_md",
		})
		return
	}
	response.Error(w, "Not found", http.StatusNotFound)
}
