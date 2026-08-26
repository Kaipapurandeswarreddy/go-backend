package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"ambigo-backend/api/middleware"
	"ambigo-backend/api/response"
	"ambigo-backend/internal/auth"
	"ambigo-backend/internal/eventbus"
	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/payment"
	"ambigo-backend/internal/requestid"

	"github.com/jackc/pgx/v5"
)

type WalletHandler struct {
	AuthStore     *auth.Store
	EventBus      *eventbus.InMemoryBus
	WalletStore   *payment.WalletStore
	ZwitchService *payment.ZwitchService
}

func NewWalletHandler(authStore *auth.Store, eventBus *eventbus.InMemoryBus, wStore *payment.WalletStore, zService *payment.ZwitchService) *WalletHandler {
	return &WalletHandler{
		AuthStore:     authStore,
		EventBus:      eventBus,
		WalletStore:   wStore,
		ZwitchService: zService,
	}
}

func (h *WalletHandler) HandleGetWallet(w http.ResponseWriter, r *http.Request) {
	uidStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	driver, err := h.AuthStore.FindDriverByID(r.Context(), uidStr)
	if err != nil || driver == nil {
		response.Error(w, "Driver not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(driver.WalletDetails)
}

func (h *WalletHandler) HandleUpdateWallet(w http.ResponseWriter, r *http.Request) {
	uidStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req auth.WalletDetails
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if !response.Validate(w, &req) {
		return
	}

	driver, err := h.AuthStore.FindDriverByID(r.Context(), uidStr)
	if err != nil || driver == nil {
		response.Error(w, "Driver not found", http.StatusNotFound)
		return
	}

	// WalletDetails is optional at driver creation (admin add); handle nil
	var dbAcc *auth.WalletDetails
	if driver.WalletDetails != nil {
		dbAcc = driver.WalletDetails
	} else {
		dbAcc = &auth.WalletDetails{}
	}
	if dbAcc.AccountNo == "" {
		// New beneficiary
		benfID, err := h.ZwitchService.CreateBeneficiary(&req, uidStr)
		if err != nil || benfID == "" {
			response.Error(w, "Zwitch Beneficiary Account Creation error", http.StatusBadRequest)
			return
		}
		req.BenfID = benfID
	} else {
		// Update existing
		if dbAcc.AccountNo == req.AccountNo {
			req.BenfID = dbAcc.BenfID
			h.ZwitchService.UpdateBeneficiaryName(&req)
		} else {
			// Account changed entirely, recreate
			benfID, err := h.ZwitchService.CreateBeneficiary(&req, uidStr)
			if err != nil || benfID == "" {
				response.Error(w, "Zwitch Beneficiary Account Creation error", http.StatusBadRequest)
				return
			}
			req.BenfID = benfID
			h.ZwitchService.DeleteBeneficiary(dbAcc.BenfID)
		}
	}

	if err := h.WalletStore.UpdateWalletDetails(r.Context(), uidStr, req); err != nil {
		response.Error(w, "Error updating wallet details", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"detail": "Wallet details updated successfully"})
}

func (h *WalletHandler) HandleWithdraw(w http.ResponseWriter, r *http.Request) {
	uidStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	reqID := requestid.FromContext(r.Context())

	var req struct {
		Amount float64 `json:"amount" validate:"required,gt=0"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if !response.Validate(w, &req) {
		return
	}

	driver, err := h.AuthStore.FindDriverByID(r.Context(), uidStr)
	if err != nil || driver == nil {
		response.Error(w, "Driver not found", http.StatusNotFound)
		return
	}

	if driver.WalletDetails == nil || driver.WalletDetails.BenfID == "" {
		response.Error(w, "Driver Account Details not found", http.StatusBadRequest)
		return
	}

	// Reference ID = random 10 chars, simplified here using timestamp
	merchantRefID := fmt.Sprintf("W%d", time.Now().UnixNano())

	// Rs. 7 fee for transaction
	amountToTransfer := req.Amount - 7
	if amountToTransfer <= 0 {
		response.Error(w, "Amount too low to cover 7rs fee", http.StatusBadRequest)
		return
	}

	// Saga Tx1: deduct balance + insert pending transaction atomically
	// Idempotency via merchant_reference_id UNIQUE (migrations 00001_init.sql:372)
	pendingTx := &payment.WalletTransaction{
		DriverID:            uidStr,
		ZwitchBeneficiaryID: driver.WalletDetails.BenfID,
		Amount:              req.Amount,
		AccountNo:           driver.WalletDetails.AccountNo,
		MerchantReferenceID: merchantRefID,
		Status:              "pending",
	}
	if err := payment.WithTx(r.Context(), h.WalletStore.Pool(), func(tx pgx.Tx) error {
		wTx := h.WalletStore.WithTx(tx)
		if err := wTx.DeductBalance(r.Context(), uidStr, req.Amount); err != nil {
			return err
		}
		return wTx.InsertTransaction(r.Context(), pendingTx)
	}); err != nil {
		response.Error(w, "Insufficient wallet balance", http.StatusBadRequest)
		return
	}

	resp, err := h.ZwitchService.CreateTransfer(driver.WalletDetails, amountToTransfer, merchantRefID)
	log := logger.Ctx(r.Context())
	if err != nil || resp == nil {
		log.Error().Err(err).Msg("Zwitch transfer failed, refunding wallet")
		// Compensating Tx2b: refund wallet + mark transaction failed
		if refundErr := payment.WithTx(r.Context(), h.WalletStore.Pool(), func(tx pgx.Tx) error {
			wTx := h.WalletStore.WithTx(tx)
			if rErr := wTx.UpdateWalletBalance(r.Context(), uidStr, req.Amount); rErr != nil {
				return rErr
			}
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			return wTx.UpdateTransactionStatus(r.Context(), merchantRefID, "failed", "", "", errMsg)
		}); refundErr != nil {
			log.Error().Err(refundErr).Msg("Refund also failed -- manual intervention required")
		}
		response.Error(w, "Withdrawal Initiation failed", http.StatusBadRequest)
		return
	}

	// Save transaction result — handle Zwitch status
	status, _ := resp["status"].(string)
	bankRef, _ := resp["bank_reference_number"].(string)
	transferID, _ := resp["id"].(string)
	errMsg := ""
	if reason, ok := resp["reason_for_error"].(string); ok {
		errMsg = reason
	}

	// If Zwitch reports failed, compensating refund
	if status == "failed" {
		log.Error().Str("merchant_ref", merchantRefID).Str("status", status).Msg("Zwitch returned failed, refunding")
		if refundErr := payment.WithTx(r.Context(), h.WalletStore.Pool(), func(tx pgx.Tx) error {
			wTx := h.WalletStore.WithTx(tx)
			if rErr := wTx.UpdateWalletBalance(r.Context(), uidStr, req.Amount); rErr != nil {
				return rErr
			}
			return wTx.UpdateTransactionStatus(r.Context(), merchantRefID, "failed", bankRef, transferID, errMsg)
		}); refundErr != nil {
			log.Error().Err(refundErr).Msg("Refund also failed -- manual intervention required")
		}
		response.Error(w, "Withdrawal Initiation failed", http.StatusBadRequest)
		return
	}

	// Success or pending — update transaction status in Tx2
	if updErr := payment.WithTx(r.Context(), h.WalletStore.Pool(), func(tx pgx.Tx) error {
		wTx := h.WalletStore.WithTx(tx)
		return wTx.UpdateTransactionStatus(r.Context(), merchantRefID, status, bankRef, transferID, errMsg)
	}); updErr != nil {
		log.Error().Err(updErr).Msg("Failed to update wallet transaction status")
	}

	h.EventBus.PublishEvent(eventbus.ChannelWalletWithdrawal, eventbus.WalletWithdrawalPayload{
		DriverID: uidStr, Amount: req.Amount, Status: status, RequestID: reqID,
	})

	json.NewEncoder(w).Encode(map[string]string{"detail": "Withdrawal initiated, amount will be transferred shortly!!"})
}

func (h *WalletHandler) HandleListTransactions(w http.ResponseWriter, r *http.Request) {
	uidStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		response.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

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
	// Allow JSON body {limit,cursor,skip} for POST compatibility.
	if r.Body != nil && r.ContentLength != 0 {
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

	var list []payment.WalletTransaction
	var err error
	if offset > 0 && cursor == "" {
		list, err = h.WalletStore.ListTransactionsWithOffset(r.Context(), uidStr, limit, offset)
	} else {
		list, err = h.WalletStore.ListTransactionsPaginated(r.Context(), uidStr, limit, cursor)
	}
	if err != nil {
		response.Error(w, "Failed to list transactions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}
