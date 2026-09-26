package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"ambigo-backend/api/response"
	"ambigo-backend/internal/pricing"
)

type RegionHandler struct {
	Regions *pricing.RegionStore
}

func NewRegionHandler(rs *pricing.RegionStore) *RegionHandler {
	return &RegionHandler{Regions: rs}
}

func (h *RegionHandler) HandleListRegions(w http.ResponseWriter, r *http.Request) {
	list, err := h.Regions.ListRegions(r.Context())
	if err != nil {
		response.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Success(w, http.StatusOK, map[string]interface{}{"regions": list})
}

// HandleSearchRegions: {query} -> ranked OSM candidates for the admin picker.
// Debounce client-side (~500ms); no polygons fetched here.
func (h *RegionHandler) HandleSearchRegions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	places, err := pricing.SearchOSMPlaces(ctx, req.Query)
	if err != nil {
		response.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	response.Success(w, http.StatusOK, map[string]interface{}{"places": places})
}

// HandleFetchRegionFromOSM saves a region. Preferred: exact {osm_type, osm_id}
// from the search picker (deterministic lookup, no ranking). Legacy:
// {city_query} ranked fetch. Circle fallback via {lat,lng,radius_m}.
// {name} defaults to matched display name / query.
func (h *RegionHandler) HandleFetchRegionFromOSM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CityQuery string  `json:"city_query"`
		OsmType   string  `json:"osm_type"`
		OsmID     int64   `json:"osm_id"`
		Name      string  `json:"name"`
		Lat       float64 `json:"lat"`
		Lng       float64 `json:"lng"`
		RadiusM   float64 `json:"radius_m"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	var cells []string
	var osmID int64
	var osmType string
	var geom []byte
	matched := ""
	res := pricing.RegionCellRes
	name := req.Name
	if req.OsmType != "" && req.OsmID != 0 {
		g, _, display, err := pricing.FetchPolygonByOsmID(ctx, req.OsmType, req.OsmID)
		if err != nil {
			response.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c, cr, ferr := pricing.FillCellsForGeometryRes(g)
		if ferr != nil || len(c) == 0 {
			response.Error(w, "Could not build H3 cover: "+ferr.Error(), http.StatusBadGateway)
			return
		}
		cells, osmID, osmType, geom, res, matched = c, req.OsmID, req.OsmType, g, cr, display
		if name == "" {
			name = display
		}
	} else {
		if name == "" {
			name = req.CityQuery
		}
		if req.CityQuery != "" {
			id, g, _, display, err := pricing.FetchPolygonFromOSM(ctx, req.CityQuery)
			if err == nil {
				if c, cr, ferr := pricing.FillCellsForGeometryRes(g); ferr == nil && len(c) > 0 {
					cells, osmID, geom, res, matched = c, id, g, cr, display
				}
			}
		}
	}
	if len(cells) == 0 {
		if req.Lat == 0 && req.Lng == 0 {
			response.Error(w, "OSM found no border for that query and no lat/lng given — retry as 'City, State, IN' or fill lat/lng/radius circle", http.StatusBadRequest)
			return
		}
		if req.RadiusM <= 0 {
			req.RadiusM = 30000
		}
		c, err := pricing.FillCellsForCircle(req.Lat, req.Lng, req.RadiusM)
		if err != nil {
			response.Error(w, "Could not resolve region (OSM miss + circle failed): "+err.Error(), http.StatusBadGateway)
			return
		}
		cells = c
	}
	if name == "" {
		response.Error(w, "name/city_query required", http.StatusBadRequest)
		return
	}
	region, err := h.Regions.CreateRegionFull(ctx, name, osmType, osmID, matched, geom, cells, res)
	if err != nil {
		response.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Success(w, http.StatusCreated, map[string]interface{}{"region": region, "cells": len(cells), "res": res, "matched": matched})
}

// HandleRefreshRegion re-pulls the stored OSM object (stale borders after
// bifurcations/merges) and rewrites cells. Circle regions (osm_id=0) cannot
// refresh — delete + re-add.
func (h *RegionHandler) HandleRefreshRegion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		response.Error(w, "id required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	existing, err := h.Regions.GetRegion(ctx, req.ID)
	if err != nil || existing == nil {
		response.Error(w, "region not found", http.StatusNotFound)
		return
	}
	if existing.OsmID == 0 || existing.OsmType == "" {
		response.Error(w, "circle region has no OSM source — delete + re-add", http.StatusBadRequest)
		return
	}
	g, _, display, err := pricing.FetchPolygonByOsmID(ctx, existing.OsmType, existing.OsmID)
	if err != nil {
		response.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	cells, res, err := pricing.FillCellsForGeometryRes(g)
	if err != nil || len(cells) == 0 {
		response.Error(w, "Could not build H3 cover: "+err.Error(), http.StatusBadGateway)
		return
	}
	updated, err := h.Regions.RefreshRegionCells(ctx, req.ID, cells, res, g)
	if err != nil {
		response.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Success(w, http.StatusOK, map[string]interface{}{"region": updated, "cells": len(cells), "res": res, "matched": display})
}

func (h *RegionHandler) HandleDeleteRegion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		response.Error(w, "id required", http.StatusBadRequest)
		return
	}
	if err := h.Regions.DeleteRegion(r.Context(), id); err != nil {
		response.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Success(w, http.StatusOK, map[string]string{"detail": "deleted"})
}

func (h *RegionHandler) HandleUpsertRegionPrice(w http.ResponseWriter, r *http.Request) {
	var p pricing.RegionPrice
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if p.RegionID == "" || p.AmbTypeID == "" {
		response.Error(w, "region_id + amb_type_id required", http.StatusBadRequest)
		return
	}
	if !response.Validate(w, &p) {
		return
	}
	if err := h.Regions.UpsertRegionPrice(r.Context(), &p); err != nil {
		response.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Success(w, http.StatusOK, map[string]string{"detail": "saved"})
}

func (h *RegionHandler) HandleGetRegionPrice(w http.ResponseWriter, r *http.Request) {
	regionID := r.URL.Query().Get("region_id")
	ambTypeID := r.URL.Query().Get("amb_type_id")
	if regionID == "" || ambTypeID == "" {
		response.Error(w, "region_id + amb_type_id required", http.StatusBadRequest)
		return
	}
	p, err := h.Regions.GetRegionPrice(r.Context(), regionID, ambTypeID)
	if err != nil {
		response.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if p == nil {
		response.Error(w, "not found, using global", http.StatusNotFound)
		return
	}
	response.Success(w, http.StatusOK, p)
}
