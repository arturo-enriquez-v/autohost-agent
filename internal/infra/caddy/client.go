// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package caddy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultAdminURL = "http://localhost:2019"

// routeIDPrefix is prepended to every domain to namespace autohost-managed routes.
const routeIDPrefix = "autohost-"

// Client talks to the Caddy admin API.
type Client struct {
	adminURL   string
	httpClient *http.Client
}

// NewClient creates a Caddy admin API client.
// adminURL defaults to http://localhost:2019 when empty.
func NewClient(adminURL string) *Client {
	if adminURL == "" {
		adminURL = defaultAdminURL
	}
	return &Client{
		adminURL:   strings.TrimRight(adminURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// IsRunning returns true if the Caddy admin API is reachable.
func (c *Client) IsRunning() bool {
	resp, err := c.httpClient.Get(c.adminURL + "/config/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// caddyRoute represents a single Caddy HTTP route object.
type caddyRoute struct {
	ID       string        `json:"@id"`
	Match    []matchBlock  `json:"match"`
	Handle   []handleBlock `json:"handle"`
	Terminal bool          `json:"terminal"`
}

type matchBlock struct {
	Host []string `json:"host"`
}

type handleBlock struct {
	Handler   string       `json:"handler"`
	Upstreams []upstream   `json:"upstreams,omitempty"`
	Routes    []caddyRoute `json:"routes,omitempty"`
}

type upstream struct {
	Dial string `json:"dial"`
}

// Route is a simplified view of a Caddy reverse-proxy route.
type Route struct {
	Domain   string `json:"domain"`
	Upstream string `json:"upstream"`
	// Managed is true when the route was created by autohost (has the autohost- @id prefix).
	Managed bool `json:"managed"`
}

// serverConfig is used to decode a single HTTP server from the Caddy config.
type serverConfig struct {
	Listen []string     `json:"listen"`
	Routes []caddyRoute `json:"routes"`
}

// extractUpstream walks a slice of handle blocks looking for the first
// reverse_proxy handler (including inside nested subroute handles).
func extractUpstream(handles []handleBlock) string {
	for _, h := range handles {
		if h.Handler == "reverse_proxy" && len(h.Upstreams) > 0 {
			return h.Upstreams[0].Dial
		}
		for _, nested := range h.Routes {
			if up := extractUpstream(nested.Handle); up != "" {
				return up
			}
		}
	}
	return ""
}

func routeID(domain string) string {
	return routeIDPrefix + strings.ReplaceAll(domain, ".", "-")
}

func buildRoute(domain, upstreamDial string) caddyRoute {
	return caddyRoute{
		ID:       routeID(domain),
		Match:    []matchBlock{{Host: []string{domain}}},
		Handle:   []handleBlock{{Handler: "reverse_proxy", Upstreams: []upstream{{Dial: upstreamDial}}}},
		Terminal: true,
	}
}

// UpsertRoute creates or replaces a reverse-proxy route for the given domain.
// upstreamDial is e.g. "localhost:3000".
func (c *Client) UpsertRoute(domain, upstreamDial string) error {
	id := routeID(domain)
	route := buildRoute(domain, upstreamDial)

	body, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("marshal route: %w", err)
	}

	// Try PUT /id/{id} first (updates existing named block).
	if err := c.putID(id, body); err == nil {
		return nil
	}

	// New route — find (or create) the HTTPS server to append to.
	serverName, err := c.findOrCreateServer()
	if err != nil {
		return fmt.Errorf("find server: %w", err)
	}

	return c.postJSON("/config/apps/http/servers/"+serverName+"/routes", body)
}

// DeleteRoute removes the reverse-proxy route for the given domain.
func (c *Client) DeleteRoute(domain string) error {
	id := routeID(domain)
	req, err := http.NewRequest(http.MethodDelete, c.adminURL+"/id/"+id, nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete /id/%s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone — idempotent
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete route %s: status %d: %s", domain, resp.StatusCode, string(b))
	}
	return nil
}

// ListRoutes returns all autohost-managed routes currently in Caddy
// (server "autohost", @id prefix "autohost-").
func (c *Client) ListRoutes() ([]Route, error) {
	all, err := c.ListAllRoutes()
	if err != nil {
		return nil, err
	}
	var out []Route
	for _, r := range all {
		if r.Managed {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListAllRoutes returns every reverse-proxy route across all Caddy HTTP servers,
// including routes defined in a Caddyfile (Managed=false) and those created by
// autohost (Managed=true).
func (c *Client) ListAllRoutes() ([]Route, error) {
	resp, err := c.httpClient.Get(c.adminURL + "/config/apps/http/servers")
	if err != nil {
		return nil, fmt.Errorf("get servers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		return []Route{}, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list servers: status %d: %s", resp.StatusCode, string(b))
	}

	var servers map[string]serverConfig
	if err := json.NewDecoder(resp.Body).Decode(&servers); err != nil {
		return nil, fmt.Errorf("decode servers: %w", err)
	}

	var out []Route
	for _, srv := range servers {
		for _, r := range srv.Routes {
			if len(r.Match) == 0 || len(r.Match[0].Host) == 0 {
				continue
			}
			upstream := extractUpstream(r.Handle)
			if upstream == "" {
				continue // not a reverse proxy route
			}
			for _, host := range r.Match[0].Host {
				out = append(out, Route{
					Domain:   host,
					Upstream: upstream,
					Managed:  strings.HasPrefix(r.ID, routeIDPrefix),
				})
			}
		}
	}
	return out, nil
}

// putID sends a PUT to /id/{id} with the given JSON body.
func (c *Client) putID(id string, body []byte) error {
	req, err := http.NewRequest(http.MethodPut, c.adminURL+"/id/"+id, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("id %s not found", id)
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("put id %s: status %d: %s", id, resp.StatusCode, string(b))
	}
	return nil
}

// postJSON sends a POST with the given JSON body to path.
func (c *Client) postJSON(path string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, c.adminURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post %s: status %d: %s", path, resp.StatusCode, string(b))
	}
	return nil
}

// findOrCreateServer returns the name of an HTTP server that listens on :443
// or :80. If one exists already (e.g. created by the Caddyfile as "srv0"), its
// name is returned and no server is created. Otherwise an "autohost" server is
// created with :443/:80 listeners.
func (c *Client) findOrCreateServer() (string, error) {
	resp, err := c.httpClient.Get(c.adminURL + "/config/apps/http/servers")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		// No HTTP app configured at all — create from scratch.
		return "autohost", c.createServer("autohost")
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("list servers: status %d: %s", resp.StatusCode, string(b))
	}

	var servers map[string]serverConfig
	if err := json.NewDecoder(resp.Body).Decode(&servers); err != nil {
		return "", fmt.Errorf("decode servers: %w", err)
	}

	// Prefer an existing server that already handles HTTPS/HTTP traffic.
	for name, srv := range servers {
		for _, addr := range srv.Listen {
			if addr == ":443" || addr == ":80" {
				return name, nil
			}
		}
	}

	// No matching server found — create "autohost".
	if _, ok := servers["autohost"]; ok {
		return "autohost", nil // already exists without standard listeners
	}
	return "autohost", c.createServer("autohost")
}

// createServer creates a minimal HTTP server with :443 and :80 listeners.
func (c *Client) createServer(name string) error {
	server := map[string]any{
		"listen": []string{":443", ":80"},
		"routes": []any{},
	}
	body, _ := json.Marshal(server)
	return c.postJSON("/config/apps/http/servers/"+name, body)
}
