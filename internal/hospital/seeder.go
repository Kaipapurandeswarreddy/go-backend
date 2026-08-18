package hospital

import (
	"context"
	"time"

	"ambigo-backend/internal/admin"
	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/places"
	"ambigo-backend/internal/translation"
)

// MaxCacheAge is how long Google Places data may be cached before it must be
// refreshed (Google Maps Platform ToS allows up to 30 days).
const MaxCacheAge = 30 * 24 * time.Hour

// ResidentSeedInterval is how often the background worker re-evaluates cities.
const ResidentSeedInterval = 24 * time.Hour

// Seeder hydrates the hospitals collection from Google Places nearby-search,
// bucketing each result into H3 cells for ring lookups.
type Seeder struct {
	Places    *places.PlacesClient
	Hospitals *admin.HospitalStore
	Cities    *admin.HospitalCityStore
	Counters  *admin.CounterStore
}

func NewSeeder(pc *places.PlacesClient, hStore *admin.HospitalStore, cityStore *admin.HospitalCityStore, counterStore *admin.CounterStore) *Seeder {
	return &Seeder{Places: pc, Hospitals: hStore, Cities: cityStore, Counters: counterStore}
}

// SeedAll refreshes every enabled city that is due for a resync. It returns the
// total number of hospital documents changed (inserted or replaced).
func (s *Seeder) SeedAll(ctx context.Context) (int, error) {
	if s.Places == nil || s.Places.APIKey == "" {
		return 0, nil
	}
	cities, err := s.Cities.ListEnabled(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, c := range cities {
		age := time.Since(c.LastFetched)
		if !c.LastFetched.IsZero() && age < MaxCacheAge {
			continue
		}
		n, err := s.seedCity(ctx, c)
		if err != nil {
			logger.Log.Error().Err(err).Str("city", c.Name).Msg("Hospital seed failed")
			continue
		}
		total += n
		// A changed seed must trigger the app's cached-list refresh.
		if n > 0 {
			if err := s.Counters.IncrementCounter(ctx, "hospitals"); err != nil {
				logger.Log.Error().Err(err).Msg("Failed to bump hospitals counter after seed")
			}
		}
	}
	return total, nil
}

func (s *Seeder) seedCity(ctx context.Context, city admin.HospitalCity) (int, error) {
	places, err := s.Places.SearchNearby(ctx, city.Lat, city.Lng, city.RadiusM, 20)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, p := range places {
		existing, err := s.Hospitals.FindByPlaceID(ctx, p.ID)
		if err != nil {
			return changed, err
		}

		hType := admin.ClassifyHospitalType(p.DisplayName, p.Types)
		hCat := admin.HospitalCategoryFromType(hType)

		if existing != nil {
			// Keep admin overrides; fill missing data; refresh metadata. The
			// classification is recomputed unless an admin locked it manually.
			if existing.TypeLocked {
				hType = existing.HospitalType
				hCat = existing.Category
			}
			if existing.Name == nil {
				existing.Name = translation.Map{"en_US": p.DisplayName}
			}
			if existing.Address == nil {
				existing.Address = translation.Map{"en_US": p.FormattedAddr}
			}
			if len(existing.Location.Coordinates) == 0 {
				existing.Location = admin.GeoJSON{Type: "Point", Coordinates: []float64{p.Lng, p.Lat}}
			}
			if len(existing.H3Cells) == 0 {
				existing.H3Cells = admin.BuildH3Cells(p.Lng, p.Lat)
			}
			if existing.Services == nil {
				existing.Services = []string{}
			}
			existing.GoogleTypes = p.Types
			existing.HospitalType = hType
			existing.Category = hCat
			existing.FetchedAt = time.Now()

			c, err := s.Hospitals.UpsertByPlaceID(ctx, existing)
			if err != nil {
				return changed, err
			}
			if c {
				changed++
			}
			continue
		}

		doc := &admin.Hospital{
			Name:         translation.Map{"en_US": p.DisplayName},
			Address:      translation.Map{"en_US": p.FormattedAddr},
			City:         translation.Map{"en_US": city.Name},
			Location:     admin.GeoJSON{Type: "Point", Coordinates: []float64{p.Lng, p.Lat}},
			Timing:       &admin.Timing{Start: "12:00 AM", End: "11:59 PM"},
			AlwaysOpen:   true,
			Services:     []string{},
			PlaceID:      p.ID,
			Source:       admin.HospitalSourceGoogle,
			FetchedAt:    time.Now(),
			H3Cells:      admin.BuildH3Cells(p.Lng, p.Lat),
			GoogleTypes:  p.Types,
			HospitalType: hType,
			Category:     hCat,
		}
		c, err := s.Hospitals.UpsertByPlaceID(ctx, doc)
		if err != nil {
			return changed, err
		}
		if c {
			changed++
		}
	}
	if err := s.Cities.MarkFetched(ctx, city.ID); err != nil {
		return changed, err
	}
	return changed, nil
}
