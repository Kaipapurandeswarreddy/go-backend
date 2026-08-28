package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"ambigo-backend/api/middleware"
	"ambigo-backend/api/response"
	"ambigo-backend/internal/admin"
	"ambigo-backend/internal/auth"
	"ambigo-backend/internal/ride"
	"ambigo-backend/internal/websocket"

	"ambigo-backend/internal/ids"
)

type HospitalDashboardHandler struct {
	RideStore     *ride.Store
	HospitalStore *admin.HospitalStore
	AdminStore    *admin.Store
	AuthStore     *auth.Store
	WSManager     *websocket.Manager
}

func NewHospitalDashboardHandler(rideStore *ride.Store, hospitalStore *admin.HospitalStore, adminStore *admin.Store, authStore *auth.Store, wsManager *websocket.Manager) *HospitalDashboardHandler {
	return &HospitalDashboardHandler{
		RideStore:     rideStore,
		HospitalStore: hospitalStore,
		AdminStore:    adminStore,
		AuthStore:     authStore,
		WSManager:     wsManager,
	}
}

func hospitalIDFromContext(r *http.Request) (string, bool) {
	hid, ok := r.Context().Value(middleware.HospitalIDKey).(string)
	if !ok || hid == "" {
		return "", false
	}
	return hid, true
}

func (h *HospitalDashboardHandler) HandleHospitalStats(w http.ResponseWriter, r *http.Request) {
	hid, ok := hospitalIDFromContext(r)
	if !ok {
		response.Error(w, "Hospital not linked to account", http.StatusBadRequest)
		return
	}
	incomingStatuses := []ride.RideStatus{ride.StatusSearching, ride.StatusAssigned, ride.StatusArrived, ride.StatusInProgress}
	incoming, _ := h.RideStore.CountRidesByHospital(r.Context(), hid, incomingStatuses)
	completed, _ := h.RideStore.CountRidesByHospital(r.Context(), hid, []ride.RideStatus{ride.StatusCompleted})
	cancelled, _ := h.RideStore.CountRidesByHospital(r.Context(), hid, []ride.RideStatus{ride.StatusCancelled})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"incoming":  incoming,
		"completed": completed,
		"cancelled": cancelled,
		"total":     incoming + completed,
	})
}

func (h *HospitalDashboardHandler) HandleHospitalIncomingRides(w http.ResponseWriter, r *http.Request) {
	hid, ok := hospitalIDFromContext(r)
	if !ok {
		response.Error(w, "Hospital not linked", http.StatusBadRequest)
		return
	}
	var req struct {
		Limit int64 `json:"limit"`
		Skip  int64 `json:"skip"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 20
	}
	statuses := []ride.RideStatus{ride.StatusSearching, ride.StatusAssigned, ride.StatusArrived, ride.StatusInProgress}
	rides, err := h.RideStore.ListRidesByHospital(r.Context(), hid, statuses, req.Limit, req.Skip)
	if err != nil {
		response.Error(w, "Failed to fetch", http.StatusInternalServerError)
		return
	}
	// Populate full chat history (drawer -> screen fix)
	if err := h.RideStore.PopulateConditionUpdates(r.Context(), rides); err != nil {
		// non-fatal: still return rides with latest_condition only
	}
	// Enrich with live driver location for the map polyline / moving marker (free, same H3 store as fleet)
	type enriched struct {
		*ride.Ride
		DriverLocation *admin.GeoJSON `json:"driver_location,omitempty"`
	}
	enrichedList := make([]enriched, 0, len(rides))
	for _, rd := range rides {
		er := enriched{Ride: rd}
		if rd.DriverID != nil && h.WSManager != nil && h.WSManager.LocStore != nil {
			if lat, lng, err := h.WSManager.LocStore.GetLocation(*rd.DriverID); err == nil {
				er.DriverLocation = &admin.GeoJSON{Type: "Point", Coordinates: []float64{lng, lat}}
			}
		}
		enrichedList = append(enrichedList, er)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(enrichedList)
}

func (h *HospitalDashboardHandler) HandleHospitalHistory(w http.ResponseWriter, r *http.Request) {
	hid, ok := hospitalIDFromContext(r)
	if !ok {
		response.Error(w, "Hospital not linked", http.StatusBadRequest)
		return
	}
	var req struct {
		Limit int64 `json:"limit"`
		Skip  int64 `json:"skip"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 20
	}
	statuses := []ride.RideStatus{ride.StatusCompleted, ride.StatusCancelled}
	rides, err := h.RideStore.ListRidesByHospital(r.Context(), hid, statuses, req.Limit, req.Skip)
	if err != nil {
		response.Error(w, "Failed to fetch", http.StatusInternalServerError)
		return
	}
	_ = h.RideStore.PopulateConditionUpdates(r.Context(), rides)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rides)
}

func (h *HospitalDashboardHandler) HandleHospitalRideDetail(w http.ResponseWriter, r *http.Request) {
	hid, ok := hospitalIDFromContext(r)
	if !ok {
		response.Error(w, "Hospital not linked", http.StatusBadRequest)
		return
	}
	// ride id from path /api/v2/hospital/rides/{id}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	rideID := parts[len(parts)-1]
	if rideID == "detail" {
		var body struct {
			RideID string `json:"ride_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rideID = body.RideID
	}
	if rideID == "" {
		response.Error(w, "ride_id required", http.StatusBadRequest)
		return
	}
	rideDoc, err := h.RideStore.GetRideByID(r.Context(), rideID)
	if err != nil || rideDoc == nil {
		response.Error(w, "Ride not found", http.StatusNotFound)
		return
	}
	if rideDoc.HospitalID == nil || *rideDoc.HospitalID != hid {
		response.Error(w, "Forbidden: different hospital", http.StatusForbidden)
		return
	}
	// Attach full timeline for chat view
	if ups, err := h.RideStore.ListConditionUpdates(r.Context(), rideDoc.ID); err == nil {
		rideDoc.ConditionUpdates = ups
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rideDoc)
}

func (h *HospitalDashboardHandler) HandleHospitalProfile(w http.ResponseWriter, r *http.Request) {
	hid, ok := hospitalIDFromContext(r)
	if !ok {
		response.Error(w, "Hospital not linked", http.StatusBadRequest)
		return
	}
	if !ids.IsValid(hid) {
		response.Error(w, "Invalid hospital ID", http.StatusBadRequest)
		return
	}
	hospital, err := h.HospitalStore.FindByID(r.Context(), hid)
	if err != nil || hospital == nil {
		response.Error(w, "Hospital not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hospital)
}

func (h *HospitalDashboardHandler) HandleUpdateHospitalProfile(w http.ResponseWriter, r *http.Request) {
	hid, ok := hospitalIDFromContext(r)
	if !ok {
		response.Error(w, "Hospital not linked", http.StatusBadRequest)
		return
	}
	if !ids.IsValid(hid) {
		response.Error(w, "Invalid hospital ID", http.StatusBadRequest)
		return
	}
	var req struct {
		Timing     *admin.Timing `json:"timing"`
		AlwaysOpen *bool         `json:"always_open"`
		Services   []string      `json:"services"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	hospital, err := h.HospitalStore.FindByID(r.Context(), hid)
	if err != nil || hospital == nil {
		response.Error(w, "Hospital not found", http.StatusNotFound)
		return
	}
	if req.Timing != nil {
		hospital.Timing = req.Timing
	}
	if req.AlwaysOpen != nil {
		hospital.AlwaysOpen = *req.AlwaysOpen
	}
	if req.Services != nil {
		hospital.Services = req.Services
	}
	if hospital.Services == nil {
		hospital.Services = []string{}
	}
	if err := h.HospitalStore.UpdateHospital(r.Context(), hospital); err != nil {
		response.Error(w, "Failed to update", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"detail": "Hospital updated"})
}

func (h *HospitalDashboardHandler) HandleHospitalAnalytics(w http.ResponseWriter, r *http.Request) {
	hid, ok := hospitalIDFromContext(r)
	if !ok {
		response.Error(w, "Hospital not linked", http.StatusBadRequest)
		return
	}
	var req struct {
		Range string `json:"range"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	days := 7
	if req.Range == "30d" {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days)
	rides, err := h.RideStore.ListRidesByHospitalSince(r.Context(), hid, since)
	if err != nil {
		response.Error(w, "Failed to fetch analytics", http.StatusInternalServerError)
		return
	}
	// Resolve ambulance type IDs to names
	nameByID := map[string]string{}
	if ambTypes, err := h.AdminStore.ListAmbulanceTypes(r.Context()); err == nil {
		for _, t := range ambTypes {
			nameByID[t.ID] = t.Name
		}
	}
	byCondition := map[string]int64{"stable": 0, "serious": 0, "critical": 0, "worsening": 0}
	byAmbulanceType := map[string]int64{}
	byDate := map[string]int64{}
	for _, ride := range rides {
		if ride.LatestCondition != nil {
			byCondition[ride.LatestCondition.Level]++
		}
		rawKey := "Unknown"
		if ride.AmbTypeID != nil && *ride.AmbTypeID != "" {
			rawKey = *ride.AmbTypeID
		}
		displayKey := rawKey
		if n, ok := nameByID[rawKey]; ok && n != "" {
			displayKey = n
		} else if rawKey == "Unknown" {
			displayKey = "Unknown"
		}
		byAmbulanceType[displayKey]++
		dateKey := ride.Time.CreatedAt.Format("2006-01-02")
		byDate[dateKey]++
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"byCondition":     byCondition,
		"byAmbulanceType": byAmbulanceType,
		"byDate":          byDate,
		"total":           len(rides),
	})
}

// HandleUpdateRideCondition is called by the user/attendant during an ongoing ride
func (h *HospitalDashboardHandler) HandleUpdateRideCondition(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	// ride id from path
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	rideID := ""
	for i, p := range parts {
		if p == "rides" && i+1 < len(parts) && parts[i+1] != "condition" {
			rideID = parts[i+1]
			break
		}
	}
	var req struct {
		Level  string `json:"level"`
		Note   string `json:"note"`
		RideID string `json:"ride_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.RideID != "" {
		rideID = req.RideID
	}
	// Fallback: if rideID still empty, try to get from already decoded req (above)
	if rideID == "" {
		response.Error(w, "ride_id required", http.StatusBadRequest)
		return
	}
	level := strings.ToLower(req.Level)
	if level == "" {
		response.Error(w, "level required: stable|serious|critical|worsening", http.StatusBadRequest)
		return
	}
	if level != "stable" && level != "serious" && level != "critical" && level != "worsening" {
		response.Error(w, "invalid level", http.StatusBadRequest)
		return
	}
	rideDoc, err := h.RideStore.GetRideByID(r.Context(), rideID)
	if err != nil || rideDoc == nil {
		response.Error(w, "Ride not found", http.StatusNotFound)
		return
	}
	source := "user"
	if role == "attendant" {
		source = "attendant"
		if ids.IsValid(userID) {
			if att, _ := h.AuthStore.FindAmbulanceAttendantByID(r.Context(), userID); att != nil && att.AssignedDriverID != nil && rideDoc.DriverID != nil {
				if *att.AssignedDriverID != *rideDoc.DriverID {
					response.Error(w, "Forbidden: not assigned to this ride's driver", http.StatusForbidden)
					return
				}
			}
		}
	} else if rideDoc.UserID != userID {
		response.Error(w, "Forbidden: not ride owner", http.StatusForbidden)
		return
	}
	if rideDoc.Status != ride.StatusAssigned && rideDoc.Status != ride.StatusArrived && rideDoc.Status != ride.StatusInProgress {
		response.Error(w, "Condition can only be updated during active ride", http.StatusBadRequest)
		return
	}
	upd := ride.ConditionUpdate{
		Level:     level,
		Severity:  ride.ConditionSeverity(level),
		Note:      req.Note,
		Source:    source,
		CreatedAt: time.Now(),
	}
	if err := h.RideStore.AppendConditionUpdate(r.Context(), rideID, upd); err != nil {
		response.Error(w, "Failed to update", http.StatusInternalServerError)
		return
	}
	// Push to driver via WS
	if rideDoc.DriverID != nil {
		h.WSManager.SendToClient("driver", *rideDoc.DriverID, "CONDITION_UPDATE", map[string]interface{}{
			"ride_id": rideID,
			"level":   level,
			"severity": upd.Severity,
			"note":    req.Note,
		})
	}
	// Push to hospital watchers if hospital linked
	if rideDoc.HospitalID != nil {
		h.WSManager.SendToRideWatchers(rideID, "CONDITION_UPDATE", map[string]interface{}{
			"ride_id": rideID,
			"level":   level,
			"severity": upd.Severity,
			"note":    req.Note,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"detail": "Condition updated", "level": level})
}

func (h *HospitalDashboardHandler) HandleAttendantHospitalContact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RideID string `json:"ride_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RideID == "" {
		response.Error(w, "ride_id required", http.StatusBadRequest)
		return
	}
	rideDoc, err := h.RideStore.GetRideByID(r.Context(), req.RideID)
	if err != nil || rideDoc == nil {
		response.Error(w, "Ride not found", http.StatusNotFound)
		return
	}
	if rideDoc.HospitalID == nil {
		response.Error(w, "Ride has no hospital", http.StatusBadRequest)
		return
	}
	hid := *rideDoc.HospitalID
	hospital, _ := h.HospitalStore.FindByID(r.Context(), hid)
	md, _ := h.AuthStore.FindHospitalMDByHospitalID(r.Context(), hid)
	var officialNumber, email, hospitalName string
	if hospital != nil {
		hospitalName = hospital.Name["en_US"]
	}
	if md != nil {
		officialNumber = md.OfficialNumber
		email = md.Email
		if hospitalName == "" {
			hospitalName = md.Name
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"hospital_id":     *rideDoc.HospitalID,
		"hospital_name":   hospitalName,
		"official_number": officialNumber,
		"email":           email,
		"address":         rideDoc.DropAddress,
	})
}

func (h *HospitalDashboardHandler) HandleAttendantCurrentRide(w http.ResponseWriter, r *http.Request) {
	attendantIDStr, _ := r.Context().Value(middleware.UserIDKey).(string)
	if attendantIDStr != "" {
		if ids.IsValid(attendantIDStr) {
			if att, _ := h.AuthStore.FindAmbulanceAttendantByID(r.Context(), attendantIDStr); att != nil && att.AssignedDriverID != nil {
				// Find current ride for the assigned driver
				ride, _ := h.RideStore.GetCurrentRide(r.Context(), *att.AssignedDriverID, "driver")
				if ride != nil {
					if ups, err := h.RideStore.ListConditionUpdates(r.Context(), ride.ID); err == nil {
						ride.ConditionUpdates = ups
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]interface{}{"found": true, "ride": ride})
					return
				}
			}
		}
	}
	// Fallback: most recent active ride (for demo / unassigned attendant)
	statuses := []ride.RideStatus{ride.StatusInProgress, ride.StatusAssigned, ride.StatusArrived, ride.StatusSearching}
	rides, err := h.RideStore.ListRidesByStatus(r.Context(), statuses, 1, 0)
	if err != nil || len(rides) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"found": false})
		return
	}
	if ups, err := h.RideStore.ListConditionUpdates(r.Context(), rides[0].ID); err == nil {
		rides[0].ConditionUpdates = ups
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"found": true, "ride": rides[0]})
}
