package zoho

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type CRMClient struct {
	apiKey     string
	oauthToken string
	baseURL    string
	httpClient *http.Client
}

type Contact struct {
	ID        string `json:"id,omitempty"`
	Email     string `json:"Email"`
	FirstName string `json:"First_Name"`
	LastName  string `json:"Last_Name"`
	Phone     string `json:"Phone,omitempty"`
	Source    string `json:"Lead_Source,omitempty"`
}

type CreateContactResponse struct {
	Data []struct {
		Code    string `json:"code"`
		Details struct {
			ID string `json:"id"`
		} `json:"details"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"data"`
}

func NewCRMClient(apiKey, oauthToken string) *CRMClient {
	return &CRMClient{
		apiKey:     apiKey,
		oauthToken: oauthToken,
		baseURL:    "https://www.zohoapis.com/crm/v3",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to create contact (status %d): %s", resp.StatusCode, string(body))
	}

	var createResp CreateContactResponse
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

	resp, err := c.httpClient.Do(req)
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

	resp, err := c.httpClient.Do(req)
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

// DeleteContact method to delete a contact
func (c *CRMClient) DeleteContact(ctx context.Context, contactID string) error {
	url := fmt.Sprintf("%s/Contacts/%s", c.baseURL, contactID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.httpClient.Do(req)
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

// SearchContacts method to search contacts by email
func (c *CRMClient) SearchContacts(ctx context.Context, email string) ([]Contact, error) {
	url := fmt.Sprintf("%s/Contacts/search?email=%s", c.baseURL, email)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.oauthToken)

	resp, err := c.httpClient.Do(req)
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
