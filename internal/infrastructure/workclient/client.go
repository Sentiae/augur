package workclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// Client communicates with the work-service to create specs programmatically.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

// NewClient creates a work-service HTTP client.
func NewClient(baseURL string, timeout time.Duration, serviceToken string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		token: serviceToken,
	}
}

// CreateSpecRequest mirrors the work-service's CreateSpecRequest.
type CreateSpecRequest struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Why            string    `json:"why"`
	Constraints    string    `json:"constraints,omitempty"`
	Priority       string    `json:"priority"`
	Status         string    `json:"status"`
	ParentSpecID   *uuid.UUID `json:"parent_spec_id,omitempty"`
	CreatedBy      uuid.UUID `json:"created_by"`
}

// CreateSpecResponse is the response from the work-service.
type CreateSpecResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SystemUserID is the deterministic UUID for automated spec creation (matches work-service).
var SystemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// CreateSpec calls the work-service to create a new spec.
func (c *Client) CreateSpec(ctx context.Context, req CreateSpecRequest) (*CreateSpecResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal spec request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/work/specs", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	// Mark as service-to-service call
	httpReq.Header.Set("X-Service-Name", "infrastructure-intelligence-service")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call work-service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("work-service returned status %d", resp.StatusCode)
	}

	var result CreateSpecResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Warn("Failed to decode work-service response (spec was likely created): %v", err)
		return nil, nil
	}

	return &result, nil
}
