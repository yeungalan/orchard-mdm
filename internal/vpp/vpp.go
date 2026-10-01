// Package vpp is a client for Apple's Apps and Books for Organizations API
// (formerly VPP), used to assign app licenses to devices.
package vpp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TokenInfo is decoded from a downloaded content token (sToken).
type TokenInfo struct {
	Token   string `json:"token"`
	ExpDate string `json:"expDate"`
	OrgName string `json:"orgName"`
}

// Expiry parses the expiration date.
func (t TokenInfo) Expiry() time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339, "2006-01-02T15:04:05Z0700"} {
		if ts, err := time.Parse(layout, t.ExpDate); err == nil {
			return ts
		}
	}
	return time.Time{}
}

// ParseToken decodes an sToken file.
func ParseToken(data []byte) (string, *TokenInfo, error) {
	raw := strings.TrimSpace(string(data))
	dec, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", nil, errors.New("not a valid Apps and Books content token (expected base64)")
	}
	var info TokenInfo
	if err := json.Unmarshal(dec, &info); err != nil || info.Token == "" {
		return "", nil, errors.New("content token does not contain token data")
	}
	return raw, &info, nil
}

// Client talks to the Apps and Books API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a client.
func New(base, token string) *Client {
	if base == "" {
		base = "https://vpp.itunes.apple.com/mdm/v2"
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// APIError is an error returned by Apple.
type APIError struct {
	Status       int
	ErrorNumber  int    `json:"errorNumber"`
	ErrorMessage string `json:"errorMessage"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("apps and books: %d %d %s", e.Status, e.ErrorNumber, e.ErrorMessage)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		e := &APIError{Status: resp.StatusCode}
		_ = json.Unmarshal(data, e)
		if e.ErrorMessage == "" {
			e.ErrorMessage = strings.TrimSpace(string(data))
		}
		return e
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ClientConfig is the token's location information.
type ClientConfig struct {
	CountryISO2ACode    string `json:"countryISO2ACode"`
	DefaultPlatform     string `json:"defaultPlatform"`
	LocationName        string `json:"locationName"`
	TokenExpirationDate string `json:"tokenExpirationDate"`
	UID                 string `json:"uId"`
	MDMInfo             *struct {
		ID       string `json:"id"`
		Metadata string `json:"metadata"`
		Name     string `json:"name"`
	} `json:"mdmInfo"`
}

// GetClientConfig reads the client configuration.
func (c *Client) GetClientConfig(ctx context.Context) (*ClientConfig, error) {
	var out ClientConfig
	return &out, c.do(ctx, http.MethodGet, "/client/config", nil, &out)
}

// ClaimToken registers this server as the token's MDM.
func (c *Client) ClaimToken(ctx context.Context, id, name string) (*ClientConfig, error) {
	var out ClientConfig
	err := c.do(ctx, http.MethodPost, "/client/config", map[string]any{"mdmInfo": map[string]string{"id": id, "metadata": "orchard-mdm", "name": name}}, &out)
	return &out, err
}

// Asset is a licensed product.
type Asset struct {
	AdamID             string   `json:"adamId"`
	PricingParam       string   `json:"pricingParam"`
	ProductType        string   `json:"productType"`
	AssignedCount      int      `json:"assignedCount"`
	AvailableCount     int      `json:"availableCount"`
	TotalCount         int      `json:"totalCount"`
	RetiredCount       int      `json:"retiredCount"`
	DeviceAssignable   bool     `json:"deviceAssignable"`
	Revocable          bool     `json:"revocable"`
	SupportedPlatforms []string `json:"supportedPlatforms"`
}

// Assets lists every licensed asset.
func (c *Client) Assets(ctx context.Context) ([]Asset, error) {
	var all []Asset
	page := 0
	for i := 0; i < 200; i++ {
		var out struct {
			Assets        []Asset `json:"assets"`
			NextPageIndex *int    `json:"nextPageIndex"`
			TotalPages    int     `json:"totalPages"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/assets?pageIndex=%d", page), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Assets...)
		if out.NextPageIndex == nil || *out.NextPageIndex <= page {
			break
		}
		page = *out.NextPageIndex
	}
	return all, nil
}

// EventResponse is returned by asynchronous operations.
type EventResponse struct {
	EventID             string `json:"eventId"`
	TokenExpirationDate string `json:"tokenExpirationDate"`
}

// Associate assigns licenses for assets to device serial numbers.
func (c *Client) Associate(ctx context.Context, adamID, pricing string, serials []string) (*EventResponse, error) {
	if pricing == "" {
		pricing = "STDQ"
	}
	var out EventResponse
	err := c.do(ctx, http.MethodPost, "/assets/associate", map[string]any{
		"assets":        []map[string]string{{"adamId": adamID, "pricingParam": pricing}},
		"serialNumbers": serials,
	}, &out)
	return &out, err
}

// Disassociate revokes licenses from device serial numbers.
func (c *Client) Disassociate(ctx context.Context, adamID, pricing string, serials []string) (*EventResponse, error) {
	if pricing == "" {
		pricing = "STDQ"
	}
	var out EventResponse
	err := c.do(ctx, http.MethodPost, "/assets/disassociate", map[string]any{
		"assets":        []map[string]string{{"adamId": adamID, "pricingParam": pricing}},
		"serialNumbers": serials,
	}, &out)
	return &out, err
}

// Assignment is a license assignment.
type Assignment struct {
	AdamID       string `json:"adamId"`
	PricingParam string `json:"pricingParam"`
	SerialNumber string `json:"serialNumber"`
	ClientUserID string `json:"clientUserId"`
}

// Assignments lists the license assignments for a device.
func (c *Client) Assignments(ctx context.Context, serial string) ([]Assignment, error) {
	var out struct {
		Assignments []Assignment `json:"assignments"`
	}
	err := c.do(ctx, http.MethodGet, "/assignments?serialNumber="+serial, nil, &out)
	return out.Assignments, err
}

// User is an Apps and Books user (user-based licensing).
type User struct {
	ClientUserID   string `json:"clientUserId"`
	Email          string `json:"email,omitempty"`
	ManagedAppleID string `json:"managedAppleId,omitempty"`
}

// CreateUsers registers users for user-based assignment. Creating an existing
// user is reported as an error by Apple and can be ignored.
func (c *Client) CreateUsers(ctx context.Context, users []User) (*EventResponse, error) {
	var out EventResponse
	err := c.do(ctx, http.MethodPost, "/users/create", map[string]any{"users": users}, &out)
	return &out, err
}

// AssociateUsers assigns licenses for an asset to users (clientUserIds).
func (c *Client) AssociateUsers(ctx context.Context, adamID, pricing string, clientUserIDs []string) (*EventResponse, error) {
	if pricing == "" {
		pricing = "STDQ"
	}
	var out EventResponse
	err := c.do(ctx, http.MethodPost, "/assets/associate", map[string]any{
		"assets":        []map[string]string{{"adamId": adamID, "pricingParam": pricing}},
		"clientUserIds": clientUserIDs,
	}, &out)
	return &out, err
}
