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

// HandleFetchRegionFromOSM: {city_query} -> fetch polygon, fill H3, save.
// {city_query, name?} name defaults to query. Circle fallback via {lat,lng,radius_m}.
func (h *RegionHandler) HandleFetchRegionFromOSM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CityQuery string  `json:"city_query"`
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
	var geom []byte
	res := pricing.RegionCellRes
	name := req.Name
	if name == "" {
		name = req.CityQuery
	}
	if req.CityQuery != "" {
		id, g, _, err := pricing.FetchPolygonFromOSM(ctx, req.CityQuery)
		if err == nil {
			if c, cr, ferr := pricing.FillCellsForGeometryRes(g); ferr == nil && len(c) > 0 {
				cells, osmID, geom, res = c, id, g, cr
			}
		}
	}
	if len(cells) == 0 {
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
	region, err := h.Regions.CreateRegionRes(ctx, name, osmID, geom, cells, res)
	if err != nil {
		response.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Success(w, http.StatusCreated, map[string]interface{}{"region": region, "cells": len(cells)})
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
