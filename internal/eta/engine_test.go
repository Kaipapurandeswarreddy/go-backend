package eta

import (
	"math"
	"testing"
	"time"

	"ambigo-backend/internal/ride"
)

func TestDistToSegment(t *testing.T) {
	// A: (12.0, 77.0), B: (12.0, 78.0)
	// P: (12.1, 77.5) -> projection is (12.0, 77.5)
	d := distToSegmentKm(12.1, 77.5, 12.0, 77.0, 12.0, 78.0)
	expected := HaversineKm(12.1, 77.5, 12.0, 77.5)
	if math.Abs(d-expected) > 0.01 {
		t.Fatalf("distToSegment mismatch: got %f, want ~%f", d, expected)
	}
}

func TestRemainingDistanceAlongPolyline(t *testing.T) {
	// 3 points in a line: P0 -> P1 -> P2
	p0 := [2]float64{12.90, 77.60}
	p1 := [2]float64{12.95, 77.60}
	p2 := [2]float64{13.00, 77.60}
	pts := [][2]float64{p0, p1, p2}

	total := HaversineKm(p0[0], p0[1], p1[0], p1[1]) + HaversineKm(p1[0], p1[1], p2[0], p2[1])

	// Driver at p0
	rem0 := remainingDistanceAlongPolylineKm(p0[0], p0[1], pts)
	if math.Abs(rem0-total) > 0.1 {
		t.Errorf("at p0: got %f, want %f", rem0, total)
	}

	// Driver at p1
	rem1 := remainingDistanceAlongPolylineKm(p1[0], p1[1], pts)
	expected1 := HaversineKm(p1[0], p1[1], p2[0], p2[1])
	if math.Abs(rem1-expected1) > 0.1 {
		t.Errorf("at p1: got %f, want %f", rem1, expected1)
	}

	// Driver at p2 (end)
	rem2 := remainingDistanceAlongPolylineKm(p2[0], p2[1], pts)
	if rem2 > 0.1 {
		t.Errorf("at p2: got %f, want ~0", rem2)
	}
}

func TestAntiJitterFilter(t *testing.T) {
	engine := NewETAEngine(nil, nil)

	// Set initial point
	p := engine.OnDriverLocation("ride1", "drv1", 12.90, 77.60, ride.StatusAssigned)
	if p == nil {
		t.Fatal("expected non-nil payload on first ping")
	}

	// Immediate jump > 500m (e.g. 5km jump in 100ms)
	time.Sleep(10 * time.Millisecond)
	pJump := engine.OnDriverLocation("ride1", "drv1", 12.95, 77.60, ride.StatusAssigned)
	if pJump != nil {
		t.Errorf("expected anti-jitter filter to drop 5km jump in 10ms, but got: %+v", pJump)
	}
}
