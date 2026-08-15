package places

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"ambigo-backend/internal/metrics"
	"ambigo-backend/internal/retry"
)

// Place is a normalized hospital result returned by the Google Places API (New).
type Place struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	FormattedAddr string   `json:"formatted_address"`
	Lat           float64  `json:"lat"`
	Lng           float64  `json:"lng"`
	Types         []string `json:"types,omitempty"`
}

// PlacesClient talks to the Google Places API (New) nearby-search endpoint.
// It mirrors the pattern of dispatch.RouteClient.
type PlacesClient struct {
	APIKey string
	APIURL string
	Client *http.Client
}

func NewPlacesClient(apiKey, apiURL string) *PlacesClient {
	return &PlacesClient{
		APIKey: apiKey,
		APIURL: apiURL,
		Client: &http.Client{
			Timeout: 8 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    20,
				IdleConnTimeout: 90 * time.Second,
			},
		},
	}
}

type searchNearbyRequest struct {
	IncludedTypes       []string `json:"includedTypes"`
	MaxResultCount      int      `json:"maxResultCount"`
	RankPreference      string   `json:"rankPreference"`
	LocationRestriction struct {
		Circle struct {
			Center struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"center"`
			Radius int64 `json:"radius"`
		} `json:"circle"`
	} `json:"locationRestriction"`
}

type searchNearbyResponse struct {
	Places []struct {
		ID          string `json:"id"`
		DisplayName struct {
			Text string `json:"text"`
		} `json:"displayName"`
		FormattedAddress string `json:"formattedAddress"`
		Location         struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"location"`
		Types []string `json:"types"`
	} `json:"places"`
}

// SearchNearby returns hospitals within radiusM of the given coordinates,
// sorted by distance. Returns nil, nil when the client has no API key.
func (p *PlacesClient) SearchNearby(ctx context.Context, lat, lng float64, radiusM int64, maxResults int) ([]Place, error) {
	if p.APIKey == "" {
		return nil, nil
	}

	var result []Place
	err := retry.Do(ctx, retry.Default, func(ctx context.Context) error {
		body := searchNearbyRequest{
			IncludedTypes:  []string{"hospital"},
			MaxResultCount: maxResults,
			RankPreference: "DISTANCE",
		}
		body.LocationRestriction.Circle.Center.Latitude = lat
		body.LocationRestriction.Circle.Center.Longitude = lng
		body.LocationRestriction.Circle.Radius = radiusM

		jsonData, err := json.Marshal(body)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, "POST", p.APIURL, bytes.NewBuffer(jsonData))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Goog-Api-Key", p.APIKey)
		req.Header.Set("X-Goog-FieldMask", "places.id,places.displayName,places.formattedAddress,places.location,places.types")

		start := time.Now()
		resp, err := p.Client.Do(req)
		metrics.ObserveGoogleAPI(time.Since(start))
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("google places api returned status: %d", resp.StatusCode)
		}

		var resData searchNearbyResponse
		if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
			return err
		}

		if len(resData.Places) == 0 {
			result = []Place{}
			return nil
		}

		result = make([]Place, 0, len(resData.Places))
		for _, rp := range resData.Places {
			result = append(result, Place{
				ID:            rp.ID,
				DisplayName:   rp.DisplayName.Text,
				FormattedAddr: rp.FormattedAddress,
				Lat:           rp.Location.Latitude,
				Lng:           rp.Location.Longitude,
				Types:         rp.Types,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return []Place{}, nil
	}
	return result, nil
}
