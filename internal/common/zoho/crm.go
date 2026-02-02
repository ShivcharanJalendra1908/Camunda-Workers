// internal/common/zoho/crm.go
package zoho

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"camunda-workers/internal/common/circuitbreaker"
)

type CRMClient struct {
	apiKey     string
	oauthToken string
	baseURL    string
	httpClient *http.Client
	cb         *circuitbreaker.CircuitBreaker

	// Fallback queue for when circuit is open
	pendingContacts chan *Contact
	pendingAccounts chan *Account
}

func (c *CRMClient) FindContactByEmail(ctx context.Context, email string) (any, any) {
	panic("unimplemented")
}

type Contact struct {
	ID          string `json:"id,omitempty"`
	Email       string `json:"Email"`
	FirstName   string `json:"First_Name"`
	LastName    string `json:"Last_Name"`
	Phone       string `json:"Phone,omitempty"`
	Source      string `json:"Lead_Source,omitempty"`
	Title       string `json:"Title,omitempty"`
	AccountName string `json:"Account_Name,omitempty"`
}

type Account struct {
	ID          string `json:"id,omitempty"`
	AccountName string `json:"Account_Name"`
	Website     string `json:"Website,omitempty"`
	Phone       string `json:"Phone,omitempty"`
	Industry    string `json:"Industry,omitempty"`
	Description string `json:"Description,omitempty"`
}

type CreateResponse struct {
	Data []struct {
		Code    string `json:"code"`
		Details struct {
			ID string `json:"id"`
		} `json:"details"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"data"`
}

func NewCRMClient(apiKey, oauthToken string, cbManager *circuitbreaker.Manager) *CRMClient {
	cb := cbManager.GetOrCreate("zoho-crm", circuitbreaker.Config{
		Name:             "zoho-crm",
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          45 * time.Second,
		MaxConcurrent:    10,
	})

	client := &CRMClient{
		apiKey:     apiKey,
		oauthToken: oauthToken,
		baseURL:    "https://www.zohoapis.com/crm/v3",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cb:              cb,
		pendingContacts: make(chan *Contact, 500),
		pendingAccounts: make(chan *Account, 500),
	}

	// Start background processor for pending contacts
	go client.processPendingContacts(context.Background())
	go client.processPendingAccounts(context.Background())

	return client
}

func (c *CRMClient) doRequest(req *http.Request) (*http.Response, error) {
	result, err := c.cb.Execute(func() (interface{}, error) {
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		// Treat 5xx as failures
		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("zoho server error %d: %s", resp.StatusCode, string(body))
		}

		return resp, nil
	})

	if err != nil {
		return nil, err
	}

	return result.(*http.Response), nil
}

func (c *CRMClient) CreateContact(ctx context.Context, contact *Contact) (string, error) {
	url := fmt.Sprintf("%s/Contacts", c.baseURL)

	payload := map[string]interface{}{
		"data": []Contact{*contact},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal contact: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	// Use doRequest with circuit breaker
	resp, err := c.doRequest(req)
	if err != nil {
		// Check if circuit breaker is open
		if err == circuitbreaker.ErrCircuitOpen {
			// Queue contact for later processing
			select {
			case c.pendingContacts <- contact:
				return "", fmt.Errorf("zoho unavailable, contact queued for creation")
			default:
				return "", fmt.Errorf("zoho unavailable and queue full, contact creation failed")
			}
		}
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to create contact (status %d): %s", resp.StatusCode, string(body))
	}

	var createResp CreateResponse
	if err := json.Unmarshal(body, &createResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(createResp.Data) == 0 {
		return "", fmt.Errorf("no data in response")
	}

	if createResp.Data[0].Status != "success" {
		return "", fmt.Errorf("contact creation failed: %s", createResp.Data[0].Message)
	}

	return createResp.Data[0].Details.ID, nil
}

func (c *CRMClient) GetContact(ctx context.Context, contactID string) (*Contact, error) {
	url := fmt.Sprintf("%s/Contacts/%s", c.baseURL, contactID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get contact (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []Contact `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("contact not found")
	}

	return &result.Data[0], nil
}

func (c *CRMClient) UpdateContact(ctx context.Context, contactID string, contact *Contact) error {
	url := fmt.Sprintf("%s/Contacts/%s", c.baseURL, contactID)

	payload := map[string]interface{}{
		"data": []Contact{*contact},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal contact: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update contact (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *CRMClient) DeleteContact(ctx context.Context, contactID string) error {
	url := fmt.Sprintf("%s/Contacts/%s", c.baseURL, contactID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete contact (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *CRMClient) SearchContacts(ctx context.Context, email string) ([]Contact, error) {
	url := fmt.Sprintf("%s/Contacts/search?email=%s", c.baseURL, email)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to search contacts (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []Contact `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Data, nil
}

func (c *CRMClient) CreateAccount(ctx context.Context, account *Account) (string, error) {
	url := fmt.Sprintf("%s/Accounts", c.baseURL)

	payload := map[string]interface{}{
		"data": []Account{*account},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal account: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	// Use doRequest with circuit breaker
	resp, err := c.doRequest(req)
	if err != nil {
		// Check if circuit breaker is open
		if err == circuitbreaker.ErrCircuitOpen {
			// Queue account for later processing
			select {
			case c.pendingAccounts <- account:
				return "", fmt.Errorf("zoho unavailable, account queued for creation")
			default:
				return "", fmt.Errorf("zoho unavailable and queue full, account creation failed")
			}
		}
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to create account (status %d): %s", resp.StatusCode, string(body))
	}

	var createResp CreateResponse
	if err := json.Unmarshal(body, &createResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(createResp.Data) == 0 {
		return "", fmt.Errorf("no data in response")
	}

	if createResp.Data[0].Status != "success" {
		return "", fmt.Errorf("account creation failed: %s", createResp.Data[0].Message)
	}

	return createResp.Data[0].Details.ID, nil
}

func (c *CRMClient) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	url := fmt.Sprintf("%s/Accounts/%s", c.baseURL, accountID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get account (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []Account `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("account not found")
	}

	return &result.Data[0], nil
}

func (c *CRMClient) UpdateAccount(ctx context.Context, accountID string, account *Account) error {
	url := fmt.Sprintf("%s/Accounts/%s", c.baseURL, accountID)

	payload := map[string]interface{}{
		"data": []Account{*account},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal account: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update account (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *CRMClient) DeleteAccount(ctx context.Context, accountID string) error {
	url := fmt.Sprintf("%s/Accounts/%s", c.baseURL, accountID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete account (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *CRMClient) SearchAccounts(ctx context.Context, accountName string) ([]Account, error) {
	url := fmt.Sprintf("%s/Accounts/search?criteria=Account_Name:equals:%s", c.baseURL, accountName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to search accounts (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []Account `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Data, nil
}

// processPendingContacts - Background worker for fallback queue
func (c *CRMClient) processPendingContacts(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only process if circuit is not open
			if c.cb.State() == circuitbreaker.StateOpen {
				continue
			}

			// Process up to 10 contacts per batch
			for i := 0; i < 10; i++ {
				select {
				case contact := <-c.pendingContacts:
					_, err := c.CreateContact(ctx, contact)
					if err != nil {
						// Put back if still failing
						select {
						case c.pendingContacts <- contact:
						default:
							// Queue full, log error
						}
					}
				default:
					// No more pending contacts
					goto nextTickContacts
				}
			}
		nextTickContacts:
		}
	}
}

// processPendingAccounts - Background worker for fallback queue
func (c *CRMClient) processPendingAccounts(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only process if circuit is not open
			if c.cb.State() == circuitbreaker.StateOpen {
				continue
			}

			// Process up to 10 accounts per batch
			for i := 0; i < 10; i++ {
				select {
				case account := <-c.pendingAccounts:
					_, err := c.CreateAccount(ctx, account)
					if err != nil {
						// Put back if still failing
						select {
						case c.pendingAccounts <- account:
						default:
							// Queue full, log error
						}
					}
				default:
					// No more pending accounts
					goto nextTickAccounts
				}
			}
		nextTickAccounts:
		}
	}
}

func (c *CRMClient) GetCircuitBreakerMetrics() map[string]interface{} {
	metrics := c.cb.Metrics()
	metrics["pending_contacts"] = len(c.pendingContacts)
	metrics["pending_accounts"] = len(c.pendingAccounts)
	return metrics
}

func (c *CRMClient) GetPendingContactCount() int {
	return len(c.pendingContacts)
}

func (c *CRMClient) GetPendingAccountCount() int {
	return len(c.pendingAccounts)
}

// package zoho

// import (
// 	"bytes"
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"net/http"
// 	"time"
// )

// type CRMClient struct {
// 	apiKey     string
// 	oauthToken string
// 	baseURL    string
// 	httpClient *http.Client
// }

// type Contact struct {
// 	ID          string `json:"id,omitempty"`
// 	Email       string `json:"Email"`
// 	FirstName   string `json:"First_Name"`
// 	LastName    string `json:"Last_Name"`
// 	Phone       string `json:"Phone,omitempty"`
// 	Source      string `json:"Lead_Source,omitempty"`
// 	Title       string `json:"Title,omitempty"`
// 	AccountName string `json:"Account_Name,omitempty"`
// }

// type Account struct {
// 	ID          string `json:"id,omitempty"`
// 	AccountName string `json:"Account_Name"`
// 	Website     string `json:"Website,omitempty"`
// 	Phone       string `json:"Phone,omitempty"`
// 	Industry    string `json:"Industry,omitempty"`
// 	Description string `json:"Description,omitempty"`
// }

// type CreateResponse struct {
// 	Data []struct {
// 		Code    string `json:"code"`
// 		Details struct {
// 			ID string `json:"id"`
// 		} `json:"details"`
// 		Message string `json:"message"`
// 		Status  string `json:"status"`
// 	} `json:"data"`
// }

// func NewCRMClient(apiKey, oauthToken string) *CRMClient {
// 	return &CRMClient{
// 		apiKey:     apiKey,
// 		oauthToken: oauthToken,
// 		baseURL:    "https://www.zohoapis.com/crm/v3",
// 		httpClient: &http.Client{
// 			Timeout: 30 * time.Second,
// 		},
// 	}
// }

// func (c *CRMClient) CreateContact(ctx context.Context, contact *Contact) (string, error) {
// 	url := fmt.Sprintf("%s/Contacts", c.baseURL)

// 	payload := map[string]interface{}{
// 		"data": []Contact{*contact},
// 	}

// 	jsonData, err := json.Marshal(payload)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to marshal contact: %w", err)
// 	}

// 	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
// 	if err != nil {
// 		return "", fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Content-Type", "application/json")
// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	body, err := io.ReadAll(resp.Body)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to read response body: %w", err)
// 	}

// 	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
// 		return "", fmt.Errorf("failed to create contact (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	var createResp CreateResponse
// 	if err := json.Unmarshal(body, &createResp); err != nil {
// 		return "", fmt.Errorf("failed to unmarshal response: %w", err)
// 	}

// 	if len(createResp.Data) == 0 {
// 		return "", fmt.Errorf("no data in response")
// 	}

// 	if createResp.Data[0].Status != "success" {
// 		return "", fmt.Errorf("contact creation failed: %s", createResp.Data[0].Message)
// 	}

// 	return createResp.Data[0].Details.ID, nil
// }

// func (c *CRMClient) GetContact(ctx context.Context, contactID string) (*Contact, error) {
// 	url := fmt.Sprintf("%s/Contacts/%s", c.baseURL, contactID)

// 	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		body, _ := io.ReadAll(resp.Body)
// 		return nil, fmt.Errorf("failed to get contact (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	var result struct {
// 		Data []Contact `json:"data"`
// 	}

// 	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
// 		return nil, fmt.Errorf("failed to decode response: %w", err)
// 	}

// 	if len(result.Data) == 0 {
// 		return nil, fmt.Errorf("contact not found")
// 	}

// 	return &result.Data[0], nil
// }

// func (c *CRMClient) UpdateContact(ctx context.Context, contactID string, contact *Contact) error {
// 	url := fmt.Sprintf("%s/Contacts/%s", c.baseURL, contactID)

// 	payload := map[string]interface{}{
// 		"data": []Contact{*contact},
// 	}

// 	jsonData, err := json.Marshal(payload)
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal contact: %w", err)
// 	}

// 	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewBuffer(jsonData))
// 	if err != nil {
// 		return fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Content-Type", "application/json")
// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		body, _ := io.ReadAll(resp.Body)
// 		return fmt.Errorf("failed to update contact (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	return nil
// }

// // DeleteContact method to delete a contact
// func (c *CRMClient) DeleteContact(ctx context.Context, contactID string) error {
// 	url := fmt.Sprintf("%s/Contacts/%s", c.baseURL, contactID)

// 	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
// 	if err != nil {
// 		return fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
// 		body, _ := io.ReadAll(resp.Body)
// 		return fmt.Errorf("failed to delete contact (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	return nil
// }

// // SearchContacts method to search contacts by email
// func (c *CRMClient) SearchContacts(ctx context.Context, email string) ([]Contact, error) {
// 	url := fmt.Sprintf("%s/Contacts/search?email=%s", c.baseURL, email)

// 	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		body, _ := io.ReadAll(resp.Body)
// 		return nil, fmt.Errorf("failed to search contacts (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	var result struct {
// 		Data []Contact `json:"data"`
// 	}

// 	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
// 		return nil, fmt.Errorf("failed to decode response: %w", err)
// 	}

// 	return result.Data, nil
// }

// // CreateAccount method to create an account
// func (c *CRMClient) CreateAccount(ctx context.Context, account *Account) (string, error) {
// 	url := fmt.Sprintf("%s/Accounts", c.baseURL)

// 	payload := map[string]interface{}{
// 		"data": []Account{*account},
// 	}

// 	jsonData, err := json.Marshal(payload)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to marshal account: %w", err)
// 	}

// 	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
// 	if err != nil {
// 		return "", fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Content-Type", "application/json")
// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	body, err := io.ReadAll(resp.Body)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to read response body: %w", err)
// 	}

// 	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
// 		return "", fmt.Errorf("failed to create account (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	var createResp CreateResponse
// 	if err := json.Unmarshal(body, &createResp); err != nil {
// 		return "", fmt.Errorf("failed to unmarshal response: %w", err)
// 	}

// 	if len(createResp.Data) == 0 {
// 		return "", fmt.Errorf("no data in response")
// 	}

// 	if createResp.Data[0].Status != "success" {
// 		return "", fmt.Errorf("account creation failed: %s", createResp.Data[0].Message)
// 	}

// 	return createResp.Data[0].Details.ID, nil
// }

// // GetAccount method to get an account
// func (c *CRMClient) GetAccount(ctx context.Context, accountID string) (*Account, error) {
// 	url := fmt.Sprintf("%s/Accounts/%s", c.baseURL, accountID)

// 	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		body, _ := io.ReadAll(resp.Body)
// 		return nil, fmt.Errorf("failed to get account (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	var result struct {
// 		Data []Account `json:"data"`
// 	}

// 	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
// 		return nil, fmt.Errorf("failed to decode response: %w", err)
// 	}

// 	if len(result.Data) == 0 {
// 		return nil, fmt.Errorf("account not found")
// 	}

// 	return &result.Data[0], nil
// }

// // UpdateAccount method to update an account
// func (c *CRMClient) UpdateAccount(ctx context.Context, accountID string, account *Account) error {
// 	url := fmt.Sprintf("%s/Accounts/%s", c.baseURL, accountID)

// 	payload := map[string]interface{}{
// 		"data": []Account{*account},
// 	}

// 	jsonData, err := json.Marshal(payload)
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal account: %w", err)
// 	}

// 	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewBuffer(jsonData))
// 	if err != nil {
// 		return fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Content-Type", "application/json")
// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		body, _ := io.ReadAll(resp.Body)
// 		return fmt.Errorf("failed to update account (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	return nil
// }

// // DeleteAccount method to delete an account
// func (c *CRMClient) DeleteAccount(ctx context.Context, accountID string) error {
// 	url := fmt.Sprintf("%s/Accounts/%s", c.baseURL, accountID)

// 	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
// 	if err != nil {
// 		return fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
// 		body, _ := io.ReadAll(resp.Body)
// 		return fmt.Errorf("failed to delete account (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	return nil
// }

// // SearchAccounts method to search accounts by name
// func (c *CRMClient) SearchAccounts(ctx context.Context, accountName string) ([]Account, error) {
// 	url := fmt.Sprintf("%s/Accounts/search?criteria=Account_Name:equals:%s", c.baseURL, accountName)

// 	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create request: %w", err)
// 	}

// 	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

// 	resp, err := c.httpClient.Do(req)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to execute request: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		body, _ := io.ReadAll(resp.Body)
// 		return nil, fmt.Errorf("failed to search accounts (status %d): %s", resp.StatusCode, string(body))
// 	}

// 	var result struct {
// 		Data []Account `json:"data"`
// 	}

// 	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
// 		return nil, fmt.Errorf("failed to decode response: %w", err)
// 	}

// 	return result.Data, nil
// }
