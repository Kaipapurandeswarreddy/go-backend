package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"ambigo-backend/api/response"
	"ambigo-backend/internal/eventbus"
	"ambigo-backend/internal/ids"
	"ambigo-backend/internal/offer"
	"ambigo-backend/internal/requestid"
)

type OfferHandler struct {
	Store    *offer.Store
	EventBus *eventbus.InMemoryBus
}

func NewOfferHandler(store *offer.Store, eventBus *eventbus.InMemoryBus) *OfferHandler {
	return &OfferHandler{Store: store, EventBus: eventBus}
}

func (h *OfferHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	reqID := requestid.FromContext(r.Context())

	var o offer.Offer
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if !response.Validate(w, &o) {
		return
	}

	if err := h.Store.Create(r.Context(), &o); err != nil {
		response.Error(w, "Failed to create offer", http.StatusInternalServerError)
		return
	}

	h.EventBus.PublishEvent(eventbus.ChannelAdminOfferCreated, eventbus.AdminOfferPayload{
		OfferID: o.ID, Description: o.Description, RequestID: reqID,
	})

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"detail": "Created",
		"id":     o.ID,
	})
}

func (h *OfferHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	cursor := r.URL.Query().Get("cursor")
	if cursor == "" {
		cursor = r.URL.Query().Get("after_id")
	}
	offset := 0
	if v := r.URL.Query().Get("skip"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	} else if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	// Allow JSON body {limit,cursor} as alternative for POST compatibility.
	if r.Body != nil && r.Method != http.MethodGet && r.ContentLength != 0 {
		var body struct {
			Limit   *int   `json:"limit"`
			Cursor  string `json:"cursor"`
			AfterID string `json:"after_id"`
			Skip    *int   `json:"skip"`
			Offset  *int   `json:"offset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Limit != nil && *body.Limit > 0 {
			n := *body.Limit
			if n > 50 {
				n = 50
			}
			limit = n
		}
		if body.Cursor != "" {
			cursor = body.Cursor
		} else if body.AfterID != "" {
			cursor = body.AfterID
		}
		if body.Skip != nil && *body.Skip >= 0 {
			offset = *body.Skip
		} else if body.Offset != nil && *body.Offset >= 0 {
			offset = *body.Offset
		}
	}
	var list []offer.Offer
	var err error
	if offset > 0 && cursor == "" {
		list, err = h.Store.ListWithOffset(r.Context(), limit, offset)
	} else {
		list, err = h.Store.ListPaginated(r.Context(), limit, cursor)
	}
	if err != nil {
		response.Error(w, "Failed to fetch offers", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *OfferHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	reqID := requestid.FromContext(r.Context())
	idStr := r.PathValue("id")
	if !ids.IsValid(idStr) {
		response.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if err := h.Store.Delete(r.Context(), idStr); err != nil {
		response.Error(w, "Failed to delete offer", http.StatusInternalServerError)
		return
	}

	h.EventBus.PublishEvent(eventbus.ChannelAdminOfferDeleted, eventbus.AdminOfferPayload{
		OfferID: idStr, RequestID: reqID,
	})

	json.NewEncoder(w).Encode(map[string]string{"detail": "Deleted"})
}
