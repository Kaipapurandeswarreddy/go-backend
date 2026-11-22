package eta

import (
	"context"
	"math"
	"sync"
	"time"

	"ambigo-backend/internal/dispatch"
	"ambigo-backend/internal/eventbus"
	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/ride"
)

// HaversineKm calculates the great-circle distance between two coordinates in km.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * (math.Pi / 180.0)
	dLon := (lon2 - lon1) * (math.Pi / 180.0)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*(math.Pi/180.0))*math.Cos(lat2*(math.Pi/180.0))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

type rideETAState struct {
	rideID               string
	driverID             string
	status               ride.RideStatus
	pickupLat, pickupLng float64
	dropLat, dropLng     float64

	// Stored initial route metrics between pickup and drop
	pickupDropDistanceKm   float64
	pickupDropDurationSecs int

	// Current segment polyline points: [][2]float64{lat, lng}
	polylinePoints        [][2]float64
	totalPolylineDistance float64 // in km
	routeDurationSecs     int

	lastRouteFetchAt     time.Time
	lastFetchLat         float64
	lastFetchLng         float64

	lastDriverLat        float64
	lastDriverLng        float64
	lastDriverPingAt     time.Time

	prevETASecs          int
	prevDistanceKm       float64
	initialized          bool
}

// Engine implements the canonical server-side ETA engine.
type Engine struct {
	routeClient *dispatch.RouteClient
	rideStore   *ride.Store

	mu     sync.RWMutex
	states map[string]*rideETAState
}

// NewETAEngine creates an instance of the ETA Engine.
func NewETAEngine(routeClient *dispatch.RouteClient, rideStore *ride.Store) *Engine {
	return &Engine{
		routeClient: routeClient,
		rideStore:   rideStore,
		states:      make(map[string]*rideETAState),
	}
}

// ClearRide removes cached ETA state when a ride ends.
func (e *Engine) ClearRide(rideID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.states, rideID)
}

// OnDriverLocation is called on each driver GPS ping for an active ride.
func (e *Engine) OnDriverLocation(rideID, driverID string, lat, lng float64, status ride.RideStatus) *eventbus.RideETAUpdatePayload {
	if rideID == "" || driverID == "" || lat == 0 || lng == 0 {
		return nil
	}

	e.mu.Lock()
	state, exists := e.states[rideID]
	if !exists {
		state = &rideETAState{
			rideID:   rideID,
			driverID: driverID,
			status:   status,
		}
		e.states[rideID] = state
	}
	e.mu.Unlock()

	now := time.Now()

	// 1. Anti-Jitter Filter: ignore spurious GPS teleportation (>500m in <3 seconds)
	if !state.lastDriverPingAt.IsZero() {
		dt := now.Sub(state.lastDriverPingAt).Seconds()
		if dt < 3.0 {
			dist := HaversineKm(state.lastDriverLat, state.lastDriverLng, lat, lng)
			if dist > 0.5 { // >500m jump
				logger.Log.Warn().Str("ride_id", rideID).Float64("jump_km", dist).Msg("ETA Engine: dropped erratic GPS jitter")
				return nil
			}
		}
	}

	state.lastDriverLat = lat
	state.lastDriverLng = lng
	state.lastDriverPingAt = now

	// 2. Initialize ride geometry if first time or status changed
	statusChanged := state.status != status
	if statusChanged {
		// Destination changes between ride stages (Pickup -> Drop):
		// Clear previous stage polyline points and previous ETA state
		state.polylinePoints = nil
		state.totalPolylineDistance = 0
		state.routeDurationSecs = 0
		state.prevETASecs = 0
		state.prevDistanceKm = 0
	}
	state.status = status

	if !state.initialized || statusChanged {
		e.initRideGeometry(state)
	}

	// 3. Determine target destination based on ride status
	targetLat, targetLng := state.pickupLat, state.pickupLng
	stageStr := "en_route_pickup"

	if status == ride.StatusArrived {
		stageStr = "at_pickup"
		// When arrived at pickup, remaining driver->pickup is 0
		hETASecs := 300 + state.pickupDropDurationSecs
		hETAKm := state.pickupDropDistanceKm
		state.prevETASecs = 0
		state.prevDistanceKm = 0
		return &eventbus.RideETAUpdatePayload{
			RideID:          rideID,
			DriverID:        driverID,
			Stage:           stageStr,
			RemainingSecs:   0,
			RemainingKm:     0,
			HospitalETASecs: hETASecs,
			HospitalETAKm:   hETAKm,
			IsTrafficAware:  true,
		}
	} else if status == ride.StatusInProgress {
		targetLat, targetLng = state.dropLat, state.dropLng
		stageStr = "en_route_hospital"
	}

	// 4. Check if we need a fresh Google Routes call
	needFetch := false
	if len(state.polylinePoints) == 0 {
		needFetch = true
	} else {
		// Check divergence: distance from driver to nearest point on polyline
		divergenceKm := pointToPolylineDistanceKm(lat, lng, state.polylinePoints)
		if divergenceKm > 0.150 { // >150 meters off polyline -> reroute needed
			needFetch = true
		} else if now.Sub(state.lastRouteFetchAt) > 45*time.Second {
			// Periodic refresh: every 45s if driver moved >= 100m
			movedSinceFetch := HaversineKm(state.lastFetchLat, state.lastFetchLng, lat, lng)
			if movedSinceFetch >= 0.100 {
				needFetch = true
			}
		}
	}

	if needFetch && e.routeClient != nil {
		e.fetchFreshRoute(state, lat, lng, targetLat, targetLng)
	}

	// 5. Compute remaining distance along polyline
	remainingKm := 0.0
	if len(state.polylinePoints) > 1 {
		remainingKm = remainingDistanceAlongPolylineKm(lat, lng, state.polylinePoints)
	} else {
		// Fallback straight line
		remainingKm = HaversineKm(lat, lng, targetLat, targetLng) * 1.35
	}

	// 6. Calculate raw duration
	rawETASecs := 0
	if state.totalPolylineDistance > 0 && state.routeDurationSecs > 0 {
		ratio := remainingKm / state.totalPolylineDistance
		if ratio > 1.0 {
			ratio = 1.0
		}
		rawETASecs = int(ratio * float64(state.routeDurationSecs))
	} else {
		// Realistic ambulance response/transit speed fallback: 38 km/h (~10.5 m/s)
		rawETASecs = int((remainingKm / 38.0) * 3600.0)
	}
	if rawETASecs < 0 {
		rawETASecs = 0
	}

	// 7. Monotonic Smoothing Filter
	smoothedETASecs := rawETASecs
	if state.prevETASecs > 0 && !needFetch {
		// Bound sudden erratic jumps:
		if rawETASecs > state.prevETASecs+30 {
			// Don't jump up by >30 seconds unless there was a full reroute
			smoothedETASecs = state.prevETASecs + 5
		} else if rawETASecs < state.prevETASecs-60 {
			// Smooth rapid drops
			smoothedETASecs = (state.prevETASecs*2 + rawETASecs) / 3
		}
	}
	state.prevETASecs = smoothedETASecs
	state.prevDistanceKm = remainingKm

	// 8. Compute Hospital ETA
	hospitalETASecs := smoothedETASecs
	hospitalETAKm := remainingKm
	if status == ride.StatusAssigned {
		// In ASSIGNED stage, hospital ETA = Driver->Pickup + 300s (on-scene) + Pickup->Hospital
		hospitalETASecs = smoothedETASecs + 300 + state.pickupDropDurationSecs
		hospitalETAKm = remainingKm + state.pickupDropDistanceKm
	}

	return &eventbus.RideETAUpdatePayload{
		RideID:          rideID,
		DriverID:        driverID,
		Stage:           stageStr,
		RemainingSecs:   smoothedETASecs,
		RemainingKm:     math.Round(remainingKm*10) / 10,
		HospitalETASecs: hospitalETASecs,
		HospitalETAKm:   math.Round(hospitalETAKm*10) / 10,
		IsTrafficAware:  true,
	}
}

func (e *Engine) initRideGeometry(state *rideETAState) {
	state.initialized = true
	if e.rideStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rd, err := e.rideStore.GetRideByID(ctx, state.rideID)
	if err != nil || rd == nil {
		return
	}

	if len(rd.Pickup.Coordinates) == 2 {
		state.pickupLng = rd.Pickup.Coordinates[0]
		state.pickupLat = rd.Pickup.Coordinates[1]
	}
	if len(rd.Drop.Coordinates) == 2 {
		state.dropLng = rd.Drop.Coordinates[0]
		state.dropLat = rd.Drop.Coordinates[1]
	}
	if rd.Route != nil {
		state.pickupDropDistanceKm = rd.Route.DistanceKm
		state.pickupDropDurationSecs = rd.Route.DurationSeconds
		// In StatusInProgress, rd.Route represents the precomputed route from Pickup to Drop (Hospital).
		// Seed the polyline geometry so the engine has accurate Google ETA immediately.
		if state.status == ride.StatusInProgress && rd.Route.Polyline != "" {
			pts := ride.DecodePolyline(rd.Route.Polyline)
			if len(pts) > 0 {
				state.polylinePoints = pts
				state.totalPolylineDistance = rd.Route.DistanceKm
				state.routeDurationSecs = rd.Route.DurationSeconds
				state.lastRouteFetchAt = time.Now()
			}
		}
	}
	state.initialized = true
}

func (e *Engine) fetchFreshRoute(state *rideETAState, originLat, originLng, destLat, destLng float64) {
	if e.routeClient == nil || (destLat == 0 && destLng == 0) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	rt, err := e.routeClient.CalculateETA(ctx, originLat, originLng, destLat, destLng)
	if err != nil || rt == nil {
		return
	}

	pts := ride.DecodePolyline(rt.Polyline)
	if len(pts) > 0 {
		state.polylinePoints = pts
		state.routeDurationSecs = rt.DurationSeconds
		state.totalPolylineDistance = rt.DistanceKm
		state.lastRouteFetchAt = time.Now()
		state.lastFetchLat = originLat
		state.lastFetchLng = originLng
	} else if len(state.polylinePoints) == 0 {
		state.polylinePoints = [][2]float64{{originLat, originLng}, {destLat, destLng}}
		state.routeDurationSecs = rt.DurationSeconds
		state.totalPolylineDistance = rt.DistanceKm
		state.lastRouteFetchAt = time.Now()
		state.lastFetchLat = originLat
		state.lastFetchLng = originLng
	}
}

// pointToPolylineDistanceKm computes the minimum distance from point to any segment of polyline.
func pointToPolylineDistanceKm(lat, lng float64, pts [][2]float64) float64 {
	if len(pts) == 0 {
		return 0
	}
	minDist := math.MaxFloat64
	for i := 0; i < len(pts)-1; i++ {
		d := distToSegmentKm(lat, lng, pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1])
		if d < minDist {
			minDist = d
		}
	}
	if minDist == math.MaxFloat64 {
		return HaversineKm(lat, lng, pts[0][0], pts[0][1])
	}
	return minDist
}

// distToSegmentKm computes distance from point P to line segment AB in km.
func distToSegmentKm(pLat, pLng, aLat, aLng, bLat, bLng float64) float64 {
	// Vector AB
	dLat := bLat - aLat
	dLng := bLng - aLng
	segLenSq := dLat*dLat + dLng*dLng

	if segLenSq == 0 {
		return HaversineKm(pLat, pLng, aLat, aLng)
	}

	// Projection parameter t = (P - A) . (B - A) / |AB|^2
	t := ((pLat-aLat)*dLat + (pLng-aLng)*dLng) / segLenSq
	if t < 0 {
		return HaversineKm(pLat, pLng, aLat, aLng)
	}
	if t > 1 {
		return HaversineKm(pLat, pLng, bLat, bLng)
	}

	projLat := aLat + t*dLat
	projLng := aLng + t*dLng
	return HaversineKm(pLat, pLng, projLat, projLng)
}

// remainingDistanceAlongPolylineKm finds the closest segment to P, projects P,
// and computes the remaining distance to the end of the polyline.
func remainingDistanceAlongPolylineKm(pLat, pLng float64, pts [][2]float64) float64 {
	if len(pts) < 2 {
		return 0
	}

	closestSegIdx := 0
	minDist := math.MaxFloat64
	closestProjLat, closestProjLng := pts[0][0], pts[0][1]

	for i := 0; i < len(pts)-1; i++ {
		aLat, aLng := pts[i][0], pts[i][1]
		bLat, bLng := pts[i+1][0], pts[i+1][1]

		dLat := bLat - aLat
		dLng := bLng - aLng
		segLenSq := dLat*dLat + dLng*dLng

		t := 0.0
		if segLenSq > 0 {
			t = ((pLat-aLat)*dLat + (pLng-aLng)*dLng) / segLenSq
		}
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}

		projLat := aLat + t*dLat
		projLng := aLng + t*dLng
		dist := HaversineKm(pLat, pLng, projLat, projLng)
		if dist < minDist {
			minDist = dist
			closestSegIdx = i
			closestProjLat = projLat
			closestProjLng = projLng
		}
	}

	// Distance from projection to end of current segment
	remaining := HaversineKm(closestProjLat, closestProjLng, pts[closestSegIdx+1][0], pts[closestSegIdx+1][1])

	// Plus all subsequent segments
	for i := closestSegIdx + 1; i < len(pts)-1; i++ {
		remaining += HaversineKm(pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1])
	}

	return remaining
}
