package admin

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// HospitalCity configures the area for which hospitals are seeded from Google.
// Stored in the Data.hospital_cities collection so cities can be added at
// runtime without redeploying.
type HospitalCity struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	Name        string             `bson:"name" json:"name"`
	Lat         float64            `bson:"lat" json:"lat"`
	Lng         float64            `bson:"lng" json:"lng"`
	RadiusM     int64              `bson:"radius_m" json:"radius_m"`
	LastFetched time.Time          `bson:"last_fetched,omitempty" json:"last_fetched,omitempty"`
	Enabled     bool               `bson:"enabled" json:"enabled"`
}

type HospitalCityStore struct {
	cities *mongo.Collection
}

func NewHospitalCityStore(db *mongo.Database) *HospitalCityStore {
	return &HospitalCityStore{
		cities: db.Collection("hospital_cities"),
	}
}

// ListEnabled returns all active cities to seed.
func (s *HospitalCityStore) ListEnabled(ctx context.Context) ([]HospitalCity, error) {
	cursor, err := s.cities.Find(ctx, bson.M{"enabled": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var list []HospitalCity
	if err = cursor.All(ctx, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []HospitalCity{}
	}
	return list, nil
}

// MarkFetched records the last successful seed time for a city.
func (s *HospitalCityStore) MarkFetched(ctx context.Context, id primitive.ObjectID) error {
	_, err := s.cities.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"last_fetched": time.Now()}})
	return err
}

// ListAll returns every configured city (enabled or not), newest first.
func (s *HospitalCityStore) ListAll(ctx context.Context) ([]HospitalCity, error) {
	cursor, err := s.cities.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var list []HospitalCity
	if err = cursor.All(ctx, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []HospitalCity{}
	}
	return list, nil
}

// GetByID returns a single city by its id.
func (s *HospitalCityStore) GetByID(ctx context.Context, id primitive.ObjectID) (*HospitalCity, error) {
	var c HospitalCity
	err := s.cities.FindOne(ctx, bson.M{"_id": id}).Decode(&c)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// Create inserts a new city.
func (s *HospitalCityStore) Create(ctx context.Context, c *HospitalCity) error {
	c.ID = primitive.NewObjectID()
	_, err := s.cities.InsertOne(ctx, c)
	return err
}

// Update replaces a city's config by id.
func (s *HospitalCityStore) Update(ctx context.Context, c *HospitalCity) error {
	filter := bson.M{"_id": c.ID}
	update := bson.M{"$set": c}
	_, err := s.cities.UpdateOne(ctx, filter, update)
	return err
}

// Delete removes a city by id.
func (s *HospitalCityStore) Delete(ctx context.Context, id primitive.ObjectID) error {
	_, err := s.cities.DeleteOne(ctx, bson.M{"_id": id})
	return err
}
