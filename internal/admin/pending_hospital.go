package admin

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type PendingHospital struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	Name            string             `bson:"name" json:"name"`
	Address         string             `bson:"address" json:"address"`
	Email           string             `bson:"email" json:"email"`
	MDNumber        string             `bson:"md_number" json:"md_number"`
	OfficialNumber  string             `bson:"official_number" json:"official_number"`
	City            string             `bson:"city" json:"city"`
	Location        *GeoJSON           `bson:"location,omitempty" json:"location,omitempty"`
	Status          string             `bson:"status" json:"status"` // pending | approved | rejected
	RejectionReason *string            `bson:"rejection_reason,omitempty" json:"rejection_reason,omitempty"`
	MDID            string             `bson:"md_id,omitempty" json:"md_id,omitempty"`
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	ReviewedAt      *time.Time         `bson:"reviewed_at,omitempty" json:"reviewed_at,omitempty"`
	ReviewedBy      *string            `bson:"reviewed_by,omitempty" json:"reviewed_by,omitempty"`
}

type PendingHospitalStore struct {
	pending *mongo.Collection
}

func NewPendingHospitalStore(db *mongo.Database) *PendingHospitalStore {
	return &PendingHospitalStore{pending: db.Collection("pending_hospitals")}
}

func (s *PendingHospitalStore) Create(ctx context.Context, p *PendingHospital) error {
	p.ID = primitive.NewObjectID()
	p.CreatedAt = time.Now()
	if p.Status == "" {
		p.Status = "pending"
	}
	_, err := s.pending.InsertOne(ctx, p)
	return err
}

func (s *PendingHospitalStore) FindByID(ctx context.Context, id primitive.ObjectID) (*PendingHospital, error) {
	var p PendingHospital
	err := s.pending.FindOne(ctx, bson.M{"_id": id}).Decode(&p)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *PendingHospitalStore) FindByMDNumber(ctx context.Context, mdNumber string) (*PendingHospital, error) {
	var p PendingHospital
	err := s.pending.FindOne(ctx, bson.M{"md_number": mdNumber, "status": "pending"}).Decode(&p)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *PendingHospitalStore) ListPending(ctx context.Context) ([]PendingHospital, error) {
	cursor, err := s.pending.Find(ctx, bson.M{"status": "pending"}, options.Find().SetSort(bson.M{"created_at": -1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var list []PendingHospital
	if err = cursor.All(ctx, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []PendingHospital{}
	}
	return list, nil
}

func (s *PendingHospitalStore) ListAll(ctx context.Context) ([]PendingHospital, error) {
	cursor, err := s.pending.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"created_at": -1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var list []PendingHospital
	if err = cursor.All(ctx, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []PendingHospital{}
	}
	return list, nil
}

func (s *PendingHospitalStore) Approve(ctx context.Context, id primitive.ObjectID, reviewerID string) error {
	now := time.Now()
	_, err := s.pending.UpdateOne(ctx, bson.M{"_id": id, "status": "pending"}, bson.M{
		"$set": bson.M{"status": "approved", "reviewed_at": now, "reviewed_by": reviewerID},
	})
	return err
}

func (s *PendingHospitalStore) Reject(ctx context.Context, id primitive.ObjectID, reviewerID, reason string) error {
	now := time.Now()
	_, err := s.pending.UpdateOne(ctx, bson.M{"_id": id, "status": "pending"}, bson.M{
		"$set": bson.M{"status": "rejected", "rejection_reason": reason, "reviewed_at": now, "reviewed_by": reviewerID},
	})
	return err
}

func (s *PendingHospitalStore) Delete(ctx context.Context, id primitive.ObjectID) error {
	_, err := s.pending.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (s *PendingHospitalStore) CountPending(ctx context.Context) (int64, error) {
	return s.pending.CountDocuments(ctx, bson.M{"status": "pending"})
}
