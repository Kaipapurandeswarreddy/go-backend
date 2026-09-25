package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"ambigo-backend/internal/ids"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uber/h3-go/v4"
)

// V2 H3 region pricing. Polygons come from OSM Nominatim once per city
// (admin time), stored as H3 cell sets at RegionCellRes. Per-ride lookup is
// a local GIN set-membership check — zero external cost.
const (
	RegionCellRes = 7
	RegionMaxCells = 20000
	osmSearchURL = "https://nominatim.openstreetmap.org/search"
)

var osmClient = &http.Client{Timeout: 15 * time.Second}

type Region struct {
	ID     string   `json:"_id"`
	Name   string   `json:"name"`
	OsmID  int64    `json:"osm_id"`
	Cells  []string `json:"cells"`
	CellRes int     `json:"cell_res"`
}

type RegionPrice struct {
	RegionID         string       `json:"region_id"`
	AmbTypeID        string       `json:"amb_type_id"`
	BaseFare         float64      `json:"base_fare"`
	PricingTier      []PricingTier `json:"pricing_tier"`
	DriverShare      float64      `json:"driver_share"`
	ListingThreshold float64      `json:"listing_threshold"`
	HelperIncluded   bool         `json:"helper_included"`
	OTPRequired      bool         `json:"otp_required"`
}

type RegionStore struct {
	pool *pgxpool.Pool
}

func NewRegionStore(pool *pgxpool.Pool) *RegionStore {
	return &RegionStore{pool: pool}
}

// ---- OSM fetch (admin time only) ----

type osmFeature struct {
	Geometry struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
	Properties struct {
		OsmID int64 `json:"osm_id"`
	} `json:"properties"`
}

// FetchPolygonFromOSM queries Nominatim for "<city>,<state>,IN" and returns
// the raw geometry JSON + OSM id. Caller converts to H3 via FillCellsForGeometry.
func FetchPolygonFromOSM(ctx context.Context, cityQuery string) (int64, []byte, string, error) {
	q := url.QueryEscape(cityQuery)
	u := fmt.Sprintf("%s?q=%s&format=geojson&polygon_geojson=1&limit=1", osmSearchURL, q)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("User-Agent", "AmbigoBackend/1.0 (pricing-region-fetch)")
	req.Header.Set("Accept", "application/json")
	resp, err := osmClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil, "", fmt.Errorf("nominatim status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, "", err
	}
	var fc struct {
		Features []osmFeature `json:"features"`
	}
	if err := json.Unmarshal(body, &fc); err != nil {
		return 0, nil, "", err
	}
	if len(fc.Features) == 0 {
		return 0, nil, "", errors.New("no boundary found for query")
	}
	f := fc.Features[0]
	geomJSON, err := json.Marshal(f.Geometry)
	if err != nil {
		return 0, nil, "", err
	}
	return f.Properties.OsmID, geomJSON, f.Geometry.Type, nil
}

// FillCellsForGeometry converts a GeoJSON geometry (Polygon/MultiPolygon,
// coordinates as [lng,lat]) to H3 cells at RegionCellRes via PolygonToCells.
func FillCellsForGeometry(geomJSON []byte) ([]string, error) {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(geomJSON, &g); err != nil {
		return nil, err
	}
	toLoop := func(ring [][]float64) h3.GeoLoop {
		loop := make(h3.GeoLoop, 0, len(ring))
		for _, pt := range ring {
			if len(pt) < 2 {
				continue
			}
			loop = append(loop, h3.NewLatLng(pt[1], pt[0]))
		}
		return loop
	}
	var polys []h3.GeoPolygon
	switch g.Type {
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(g.Coordinates, &rings); err != nil {
			return nil, err
		}
		if len(rings) == 0 {
			return nil, errors.New("empty polygon")
		}
		p := h3.GeoPolygon{GeoLoop: toLoop(rings[0])}
		for _, h := range rings[1:] {
			p.Holes = append(p.Holes, toLoop(h))
		}
		polys = append(polys, p)
	case "MultiPolygon":
		var multi [][][][]float64
		if err := json.Unmarshal(g.Coordinates, &multi); err != nil {
			return nil, err
		}
		for _, rings := range multi {
			if len(rings) == 0 {
				continue
			}
			p := h3.GeoPolygon{GeoLoop: toLoop(rings[0])}
			for _, h := range rings[1:] {
				p.Holes = append(p.Holes, toLoop(h))
			}
			polys = append(polys, p)
		}
	default:
		return nil, fmt.Errorf("unsupported geometry %s", g.Type)
	}
	seen := make(map[string]bool)
	var out []string
	for _, p := range polys {
		cells, err := h3.PolygonToCells(p, RegionCellRes)
		if err != nil {
			return nil, err
		}
		for _, c := range cells {
			s := c.String()
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
			if len(out) > RegionMaxCells {
				return nil, fmt.Errorf("region too large (%d cells), simplify polygon or lower res", len(out))
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no cells filled")
	}
	return out, nil
}

// FillCellsForCircle fallback when OSM has no boundary: GridDisk cover filtered by haversine.
func FillCellsForCircle(lat, lng, radiusM float64) ([]string, error) {
	edgeM, err := h3.HexagonEdgeLengthAvgM(RegionCellRes)
	if err != nil || edgeM <= 0 {
		edgeM = 1000
	}
	k := int(math.Ceil(radiusM/edgeM)) + 1
	if k < 1 {
		k = 1
	}
	if k > 100 {
		return nil, fmt.Errorf("radius too large for res %d", RegionCellRes)
	}
	center, err := h3.LatLngToCell(h3.NewLatLng(lat, lng), RegionCellRes)
	if err != nil {
		return nil, err
	}
	disk, err := h3.GridDisk(center, k)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range disk {
		ll, err := h3.CellToLatLng(c)
		if err != nil {
			continue
		}
		dLat := (ll.Lat - lat) * math.Pi / 180.0
		dLng := (ll.Lng - lng) * math.Pi / 180.0
		a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat*math.Pi/180.0)*math.Cos(ll.Lat*math.Pi/180.0)*math.Sin(dLng/2)*math.Sin(dLng/2)
		if 6371000*2*math.Atan2(math.Sqrt(a), math.Sqrt(1-a)) <= radiusM {
			out = append(out, c.String())
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no cells in circle")
	}
	return out, nil
}

// ---- CRUD ----

func (s *RegionStore) CreateRegion(ctx context.Context, name string, osmID int64, polygonJSON []byte, cells []string) (*Region, error) {
	r := &Region{ID: ids.New(), Name: name, OsmID: osmID, Cells: cells, CellRes: RegionCellRes}
	polyArg := []byte(`{}`)
	if len(polygonJSON) > 0 {
		polyArg = polygonJSON
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO pricing_regions (id, name, osm_id, polygon, cells, cell_res) VALUES ($1::uuid, $2, $3, $4::jsonb, $5::text[], $6)`,
		r.ID, r.Name, r.OsmID, polyArg, r.Cells, r.CellRes)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *RegionStore) ListRegions(ctx context.Context) ([]Region, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, name, osm_id, cells, cell_res FROM pricing_regions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Region
	for rows.Next() {
		var r Region
		if err := rows.Scan(&r.ID, &r.Name, &r.OsmID, &r.Cells, &r.CellRes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []Region{}
	}
	return out, rows.Err()
}

func (s *RegionStore) DeleteRegion(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM pricing_regions WHERE id=$1::uuid`, id)
	return err
}

// FindRegionForPickup returns smallest matching region (smallest wins on overlap).
func (s *RegionStore) FindRegionForPickup(ctx context.Context, lat, lng float64) (*Region, error) {
	cell, err := h3.LatLngToCell(h3.NewLatLng(lat, lng), RegionCellRes)
	if err != nil {
		return nil, err
	}
	row := s.pool.QueryRow(ctx,
		`SELECT id::text, name, osm_id, cells, cell_res FROM pricing_regions WHERE cells @> $1::text[] ORDER BY array_length(cells, 1) ASC LIMIT 1`,
		[]string{cell.String()})
	var r Region
	if err := row.Scan(&r.ID, &r.Name, &r.OsmID, &r.Cells, &r.CellRes); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *RegionStore) UpsertRegionPrice(ctx context.Context, p *RegionPrice) error {
	tiersJSON, err := json.Marshal(p.PricingTier)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO region_prices (region_id, amb_type_id, base_fare, pricing_tier, driver_share, listing_threshold, helper_included, otp_required, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5, $6, $7, $8, now())
		 ON CONFLICT (region_id, amb_type_id) DO UPDATE SET base_fare=EXCLUDED.base_fare, pricing_tier=EXCLUDED.pricing_tier, driver_share=EXCLUDED.driver_share, listing_threshold=EXCLUDED.listing_threshold, helper_included=EXCLUDED.helper_included, otp_required=EXCLUDED.otp_required, updated_at=now()`,
		p.RegionID, p.AmbTypeID, p.BaseFare, tiersJSON, p.DriverShare, p.ListingThreshold, p.HelperIncluded, p.OTPRequired)
	return err
}

func (s *RegionStore) GetRegionPrice(ctx context.Context, regionID, ambTypeID string) (*RegionPrice, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT region_id::text, amb_type_id::text, base_fare, pricing_tier, driver_share, listing_threshold, helper_included, otp_required FROM region_prices WHERE region_id=$1::uuid AND amb_type_id=$2::uuid`,
		regionID, ambTypeID)
	var p RegionPrice
	var tiersJSON []byte
	if err := row.Scan(&p.RegionID, &p.AmbTypeID, &p.BaseFare, &tiersJSON, &p.DriverShare, &p.ListingThreshold, &p.HelperIncluded, &p.OTPRequired); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if len(tiersJSON) > 0 {
		_ = json.Unmarshal(tiersJSON, &p.PricingTier)
	}
	return &p, nil
}

// ResolvedPrice is the effective fare config for a pickup.
type ResolvedPrice struct {
	RegionID         *string
	BaseFare         float64
	Tiers            []PricingTier
	DriverShare      float64
	ListingThreshold float64
	HelperIncluded   bool
	OTPRequired      bool
	IsOverride       bool
}

// ResolvePriceForPickup finds the smallest H3 region containing the pickup
// and returns its price override for the ambulance type. Falls back to the
// supplied global config when no region or no override exists.
func (s *RegionStore) ResolvePriceForPickup(ctx context.Context, lat, lng float64, ambTypeID string, globalBase float64, globalTiers []PricingTier, globalShare, globalListing float64, globalHelper, globalOTP bool) (*ResolvedPrice, error) {
	base := &ResolvedPrice{
		BaseFare: globalBase, Tiers: globalTiers, DriverShare: globalShare,
		ListingThreshold: globalListing, HelperIncluded: globalHelper, OTPRequired: globalOTP,
	}
	region, err := s.FindRegionForPickup(ctx, lat, lng)
	if err != nil || region == nil {
		return base, err
	}
	override, err := s.GetRegionPrice(ctx, region.ID, ambTypeID)
	if err != nil || override == nil {
		return base, err
	}
	rid := region.ID
	return &ResolvedPrice{
		RegionID: &rid, BaseFare: override.BaseFare, Tiers: override.PricingTier,
		DriverShare: override.DriverShare, ListingThreshold: override.ListingThreshold,
		HelperIncluded: override.HelperIncluded, OTPRequired: override.OTPRequired, IsOverride: true,
	}, nil
}
