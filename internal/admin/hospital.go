package admin

import (
	"context"
	"time"

	"ambigo-backend/internal/location"
	"ambigo-backend/internal/translation"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GeoJSON is the standard MongoDB GeoJSON Point (lng-first coordinates).
type GeoJSON struct {
	Type        string    `bson:"type" json:"type"`
	Coordinates []float64 `bson:"coordinates" json:"coordinates"`
}

// Timing is the daily open/close window (24h string format, e.g. "10:00 AM").
type Timing struct {
	Start string `bson:"start" json:"start"`
	End   string `bson:"end" json:"end"`
}

// HospitalSource distinguishes admin-curated hospitals from Google-seeded ones.
const (
	HospitalSourceAdmin  = "admin"
	HospitalSourceGoogle = "google"
)

// Hospital types used for the Emergency / Non-Emergency split.
const (
	HospitalTypeGovernment      = "government"
	HospitalTypeMultiSpeciality = "multi_speciality"
	HospitalTypePrivate         = "private"
	HospitalTypeClinic          = "clinic"
	HospitalTypeGeneral         = "general"
)

// Hospital categories exposed to the app.
const (
	HospitalCategoryEmergency    = "emergency"
	HospitalCategoryNonEmergency = "non_emergency"
)

// IsEmergencyCategory reports whether a category should appear in Emergency.
func IsEmergencyCategory(category string) bool {
	return category == HospitalCategoryEmergency
}

// HospitalResolution / ring used to bucket hospitals into H3 cells for fast
// ring lookups (~25-30km coverage with ring-2 at resolution 5).
const (
	HospitalH3Resolution = 5
	HospitalH3Ring       = 2
)

// Hospital is the shared hospital document. Extra fields (place_id, h3_cells,
// source, fetched_at) are ignored by the mobile app but power the H3 lookup.
type Hospital struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	Name       translation.Map    `bson:"name" json:"name"`
	Address    translation.Map    `bson:"address" json:"address"`
	City       translation.Map    `bson:"city" json:"city"`
	Location   GeoJSON            `bson:"location" json:"location"`
	Timing     *Timing            `bson:"timing,omitempty" json:"timing,omitempty"`
	AlwaysOpen bool               `bson:"always_open" json:"always_open"`
	Services   []string           `bson:"services" json:"services"`
	PlaceID      string             `bson:"place_id,omitempty" json:"place_id,omitempty"`
	H3Cells      []string           `bson:"h3_cells,omitempty" json:"h3_cells,omitempty"`
	Source       string             `bson:"source,omitempty" json:"source,omitempty"`
	FetchedAt    time.Time          `bson:"fetched_at,omitempty" json:"fetched_at,omitempty"`
	DistanceKm   float64            `bson:"-" json:"distance_km,omitempty"`
	HospitalType string             `bson:"hospital_type,omitempty" json:"hospital_type,omitempty"`
	Category     string             `bson:"category,omitempty" json:"category,omitempty"`
	GoogleTypes  []string           `bson:"google_types,omitempty" json:"google_types,omitempty"`
	TypeLocked   bool               `bson:"type_locked,omitempty" json:"type_locked,omitempty"`
}

// BuildH3Cells computes the H3 cell(s) covering the hospital's location.
func BuildH3Cells(lng, lat float64) []string {
	cell := location.GetH3CellAtResolution(lat, lng, HospitalH3Resolution)
	if cell == "" {
		return []string{}
	}
	return []string{cell}
}

type HospitalStore struct {
	hospitals *mongo.Collection
}

func NewHospitalStore(db *mongo.Database) *HospitalStore {
	return &HospitalStore{
		hospitals: db.Collection("hospitals"),
	}
}

func (s *HospitalStore) CreateHospital(ctx context.Context, h *Hospital) error {
	h.ID = primitive.NewObjectID()
	if len(h.H3Cells) == 0 {
		h.H3Cells = BuildH3Cells(h.Location.Coordinates[0], h.Location.Coordinates[1])
	}
	_, err := s.hospitals.InsertOne(ctx, h)
	return err
}

func (s *HospitalStore) ListHospitals(ctx context.Context) ([]Hospital, error) {
	cursor, err := s.hospitals.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var list []Hospital
	if err = cursor.All(ctx, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Hospital{}
	}
	return list, nil
}

// FindByCells returns hospitals bucketed into any of the given H3 cells.
func (s *HospitalStore) FindByCells(ctx context.Context, cells []string) ([]Hospital, error) {
	cursor, err := s.hospitals.Find(ctx, bson.M{"h3_cells": bson.M{"$in": cells}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var list []Hospital
	if err = cursor.All(ctx, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Hospital{}
	}
	return list, nil
}

// FindByPlaceID looks up a hospital by its Google place_id (dedup key).
func (s *HospitalStore) FindByPlaceID(ctx context.Context, placeID string) (*Hospital, error) {
	var h Hospital
	err := s.hospitals.FindOne(ctx, bson.M{"place_id": placeID}).Decode(&h)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

// UpsertByPlaceID inserts or replaces a Google-sourced hospital keyed by
// place_id. Returns true when the document was inserted or modified.
func (s *HospitalStore) UpsertByPlaceID(ctx context.Context, h *Hospital) (changed bool, err error) {
	if len(h.H3Cells) == 0 {
		h.H3Cells = BuildH3Cells(h.Location.Coordinates[0], h.Location.Coordinates[1])
	}
	filter := bson.M{"place_id": h.PlaceID}
	update := bson.M{"$set": h}
	opts := options.Update().SetUpsert(true)
	res, err := s.hospitals.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return false, err
	}
	return res.UpsertedCount > 0 || res.ModifiedCount > 0, nil
}

func (s *HospitalStore) UpdateHospital(ctx context.Context, h *Hospital) error {
	filter := bson.M{"_id": h.ID}
	update := bson.M{"$set": h}
	_, err := s.hospitals.UpdateOne(ctx, filter, update)
	return err
}

func (s *HospitalStore) FindByID(ctx context.Context, id primitive.ObjectID) (*Hospital, error) {
	var h Hospital
	err := s.hospitals.FindOne(ctx, bson.M{"_id": id}).Decode(&h)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

func (s *HospitalStore) DeleteHospital(ctx context.Context, id primitive.ObjectID) error {
	_, err := s.hospitals.DeleteOne(ctx, bson.M{"_id": id})
	return err
}
