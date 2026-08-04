package payment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"ambigo-backend/internal/auth"
	"ambigo-backend/internal/logger"
)

type PayuService struct {
	clientID          string
	clientSecret      string
	payoutMerchantID  string
	authBaseURL       string
	apiBaseURL        string
	client            *http.Client

	mu              sync.RWMutex
	accessToken     string
	tokenExpiresAt  time.Time
}

func NewPayuService(clientID, clientSecret, payoutMerchantID, authBaseURL, apiBaseURL string) *PayuService {
	return &PayuService{
		clientID:         clientID,
		clientSecret:     clientSecret,
		payoutMerchantID: payoutMerchantID,
		authBaseURL:      authBaseURL,
		apiBaseURL:       apiBaseURL,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (s *PayuService) getToken() (string, error) {
	s.mu.RLock()
	if s.accessToken != "" && time.Now().Before(s.tokenExpiresAt) {
		token := s.accessToken
		s.mu.RUnlock()
		return token, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if s.accessToken != "" && time.Now().Before(s.tokenExpiresAt) {
		return s.accessToken, nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", s.clientID)
	form.Set("client_secret", s.clientSecret)
	form.Set("scope", "create_payout_transactions")

	req, err := http.NewRequest("POST", s.authBaseURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("cache-control", "no-cache")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("payu auth failed: %d - %s", resp.StatusCode, string(body))
	}

	var data struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	s.accessToken = data.AccessToken
	s.tokenExpiresAt = time.Now().Add(time.Duration(data.ExpiresIn) * time.Second)

	return s.accessToken, nil
}

type payuBeneficiaryResponse struct {
	Status int                    `json:"status"`
	Msg    string                 `json:"msg"`
	Code   *string                `json:"code"`
	Data   map[string]interface{} `json:"data"`
}

func (s *PayuService) CreateBeneficiary(acc *auth.WalletDetails, driverID string) (string, error) {
	token, err := s.getToken()
	if err != nil {
		return "", err
	}

	url := s.apiBaseURL + "/beneficiary"
	payload := map[string]interface{}{
		"name":    acc.BenfName,
		"accountNo": acc.AccountNo,
		"ifsc":    acc.IFSCCode,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("payoutMerchantId", s.payoutMerchantID)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result payuBeneficiaryResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("payu parse beneficiary response failed: %s", string(respBody))
	}

	if result.Status != 0 {
		return "", fmt.Errorf("payu create beneficiary failed: status=%d msg=%s", result.Status, result.Msg)
	}

	benfID := ""
	if id, ok := result.Data["beneficiaryId"]; ok {
		switch v := id.(type) {
		case float64:
			benfID = fmt.Sprintf("%.0f", v)
		case string:
			benfID = v
		}
	}
	if benfID == "" {
		return "", errors.New("payu create beneficiary: missing beneficiaryId in response")
	}

	return benfID, nil
}

func (s *PayuService) UpdateBeneficiaryName(acc *auth.WalletDetails) error {
	// PayU does not have a direct update beneficiary endpoint in the documented API.
	// Re-create the beneficiary to update the name, then store the new ID.
	newID, err := s.CreateBeneficiary(acc, "")
	if err != nil {
		return err
	}
	acc.BenfID = newID
	return nil
}

func (s *PayuService) DeleteBeneficiary(benfID string) error {
	// PayU does not expose a delete beneficiary endpoint in the documented API.
	// Beneficiaries can be managed via the PayU dashboard.
	logger.Log.Warn().Str("beneficiaryId", benfID).Msg("PayU does not support delete beneficiary via API; manage via dashboard")
	return nil
}

type payuTransferResponse struct {
	Status int                      `json:"status"`
	Msg    string                   `json:"msg"`
	Code   *string                  `json:"code"`
	Data   []map[string]interface{} `json:"data"`
}

func (s *PayuService) CreateTransfer(acc *auth.WalletDetails, amount float64, referenceID string) (map[string]interface{}, error) {
	token, err := s.getToken()
	if err != nil {
		return nil, err
	}

	url := s.apiBaseURL + "/payment"
	payload := []map[string]interface{}{
		{
			"beneficiaryAccountNumber": acc.AccountNo,
			"beneficiaryIfscCode":      acc.IFSCCode,
			"beneficiaryName":          acc.BenfName,
			"amount":                   amount,
			"merchantRefId":            referenceID,
			"paymentType":              "NEFT",
			"purpose":                  "Driver payout",
			"retry":                    false,
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("pid", s.payoutMerchantID)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result payuTransferResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("payu parse transfer response failed: %s", string(respBody))
	}

	// Build a response map matching what wallet handler expects
	responseMap := map[string]interface{}{
		"status":                "pending",
		"merchant_reference_id": referenceID,
	}

	if result.Status != 0 {
		// Validation error — extract first error if present
		if len(result.Data) > 0 {
			if errMsg, ok := result.Data[0]["error"]; ok {
				responseMap["status"] = "failed"
				responseMap["reason_for_error"] = errMsg
			}
		}
		return responseMap, fmt.Errorf("payu transfer failed: status=%d msg=%s", result.Status, result.Msg)
	}

	// Async success — status will be updated via webhook
	responseMap["id"] = referenceID
	responseMap["bank_reference_number"] = ""

	return responseMap, nil
}
