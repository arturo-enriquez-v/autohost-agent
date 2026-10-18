// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Client handles HTTP communication with the Autohost API.
type Client struct {
	baseURL      string
	token        string
	refreshToken string
	http         *http.Client
	mu           sync.RWMutex
}

// NewClient creates a new API client.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NewClientWithRefresh creates a new API client with refresh token support.
func NewClientWithRefresh(baseURL, token, refreshToken string) *Client {
	return &Client{
		baseURL:      baseURL,
		token:        token,
		refreshToken: refreshToken,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// post performs an authenticated POST request to the given path.
func (c *Client) post(ctx context.Context, path string, body any) error {
	bs, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(bs))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()
	
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Handle 401 Unauthorized - try to refresh token
	if resp.StatusCode == 401 && c.refreshToken != "" && path != EndpointRefreshToken {
		if refreshErr := c.refreshAccessToken(ctx); refreshErr == nil {
			// Retry the request with new token
			return c.post(ctx, path, body)
		}
	}

	if resp.StatusCode >= 300 {
		return &ErrStatus{Code: resp.StatusCode}
	}

	return nil
}

// RefreshTokenResponse represents the response from the refresh endpoint.
type RefreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// refreshAccessToken uses the refresh token to get a new access token.
func (c *Client) refreshAccessToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.refreshToken == "" {
		return &ErrStatus{Code: 401}
	}

	payload := map[string]string{
		"refresh_token": c.refreshToken,
	}

	bs, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+EndpointRefreshToken, bytes.NewReader(bs))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return &ErrStatus{Code: resp.StatusCode}
	}

	var refreshResp RefreshTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshResp); err != nil {
		return err
	}

	// Update tokens
	c.token = refreshResp.AccessToken
	c.refreshToken = refreshResp.RefreshToken

	return nil
}

// GetTokens returns current access and refresh tokens (thread-safe).
func (c *Client) GetTokens() (accessToken, refreshToken string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token, c.refreshToken
}

// ReportVersion notifies the API of the agent's current version.
// Called once at startup so the frontend always reflects the running version.
func (c *Client) ReportVersion(ctx context.Context, version string) error {
	return c.post(ctx, EndpointVersionReport, map[string]string{"version": version})
}
