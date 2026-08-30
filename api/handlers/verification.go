package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"ambigo-backend/api/middleware"
	"ambigo-backend/api/response"
	"ambigo-backend/internal/auth"
	"ambigo-backend/internal/ids"
	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/storage"
)

type VerificationHandler struct {
	AuthStore  *auth.Store
	StorageSvc *storage.StorageService
}

func NewVerificationHandler(authStore *auth.Store, storageSvc *storage.StorageService) *VerificationHandler {
	return &VerificationHandler{
		AuthStore:  authStore,
		StorageSvc: storageSvc,
	}
}

// HandleCheckVerification returns true if the driver is fully verified, false if unverified
// Mirrors the V1 "/check" endpoint
func (h *VerificationHandler) HandleCheckVerification(w http.ResponseWriter, r *http.Request) {
	uidStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !ids.IsValid(uidStr) {
		response.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	// First, check the active drivers collection
	driver, err := h.AuthStore.FindDriverByID(r.Context(), uidStr)
	if err != nil {
		response.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if driver != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(true) // True = verified
		return
	}

	// Next, check the unverified drivers collection
	unverified, err := h.AuthStore.FindUnverifiedDriverByID(r.Context(), uidStr)
	if err != nil {
		response.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if unverified != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(false) // False = unverified
		return
	}

	// V15: Don't leak user existence — return same 404 regardless
	response.Error(w, "Not found", http.StatusNotFound)
}

// HandleUpdateVerification handles the document upload pipeline for drivers
func (h *VerificationHandler) HandleUpdateVerification(w http.ResponseWriter, r *http.Request) {
	uidStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !ids.IsValid(uidStr) {
		response.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	var req auth.VerificationUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			response.Error(w, "Images too large: must be <10 MB total, please re-pick smaller images", http.StatusRequestEntityTooLarge)
			return
		}
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if !response.Validate(w, &req) {
		return
	}

	driver, err := h.AuthStore.FindUnverifiedDriverByID(r.Context(), uidStr)
	if err != nil {
		response.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	if driver == nil {
		response.Error(w, "Driver not found", http.StatusNotFound)
		return
	}

	// Upload Base64 images to Google Cloud Storage (GCS) and store HTTPS URLs in PostgreSQL
	portraitURL, err := h.uploadDoc(r, driver.ID, "portrait", req.PortraitImage)
	if err != nil {
		response.Error(w, "Failed to upload portrait image: "+err.Error(), http.StatusInternalServerError)
		return
	}
	poiURL, err := h.uploadDoc(r, driver.ID, "poi", req.POIImage)
	if err != nil {
		response.Error(w, "Failed to upload POI image: "+err.Error(), http.StatusInternalServerError)
		return
	}
	dlURL, err := h.uploadDoc(r, driver.ID, "dl", req.DLImage)
	if err != nil {
		response.Error(w, "Failed to upload DL image: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rcURL, err := h.uploadDoc(r, driver.ID, "rc", req.RCImage)
	if err != nil {
		response.Error(w, "Failed to upload RC image: "+err.Error(), http.StatusInternalServerError)
		return
	}
	ambFrontURL, err := h.uploadDoc(r, driver.ID, "amb_front", req.AmbFront)
	if err != nil {
		response.Error(w, "Failed to upload ambulance front image: "+err.Error(), http.StatusInternalServerError)
		return
	}
	ambInsideURL, err := h.uploadDoc(r, driver.ID, "amb_inside", req.AmbInside)
	if err != nil {
		response.Error(w, "Failed to upload ambulance inside image: "+err.Error(), http.StatusInternalServerError)
		return
	}

	driver.PortraitImage = portraitURL
	driver.POIImage = poiURL
	driver.DLImage = dlURL
	driver.RCImage = rcURL
	driver.AmbFront = ambFrontURL
	driver.AmbInside = ambInsideURL
	driver.UnderProgress = true
	driver.ErrorMessage = nil

	err = h.AuthStore.UpdateUnverifiedDriver(r.Context(), driver)
	if err != nil {
		response.Error(w, "Failed to update driver details", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"detail": "Details updated successfully and recheck initialized"})
}

func (h *VerificationHandler) uploadDoc(r *http.Request, driverID, docType, data string) (string, error) {
	if h.StorageSvc != nil {
		objectPath := fmt.Sprintf("drivers/%s/%s.jpg", driverID, docType)
		url, err := h.StorageSvc.UploadBase64IfImage(r.Context(), objectPath, data)
		if err != nil {
			logger.Log.Error().Err(err).Str("driver_id", driverID).Str("doc_type", docType).Msg("GCS upload failed")
			return "", err
		}
		return url, nil
	}
	// Fallback to data as-is if GCS client is nil
	return data, nil
}
