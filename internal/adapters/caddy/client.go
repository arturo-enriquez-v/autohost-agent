// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package caddy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	TLSMode  string `json:"tls_mode,omitempty"`
	// Managed is true when the route was created by autohost (has the autohost- @id prefix).
	Managed bool `json:"managed"`
}

type autoHTTPSConfig struct {
	Disable          bool     `json:"disable,omitempty"`
	DisableRedirects bool     `json:"disable_redirects,omitempty"`
	Skip             []string `json:"skip,omitempty"`
	SkipCerts        []string `json:"skip_certificates,omitempty"`
}

// serverConfig is used to decode a single HTTP server from the Caddy config.
type serverConfig struct {
	Listen         []string         `json:"listen"`
	Routes         []caddyRoute     `json:"routes"`
	AutomaticHTTPS *autoHTTPSConfig `json:"automatic_https,omitempty"`
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

// IsTailscaleDomain returns true when the domain is a Tailscale MagicDNS hostname (*.ts.net).
func IsTailscaleDomain(domain string) bool {
	d := strings.ToLower(strings.TrimSpace(domain))
	return strings.HasSuffix(d, ".ts.net")
}

// IsLocalOrPrivateDomain returns true when the domain is a local/homelab suffix
// (.local, .lan, .home, .home.arpa, .internal, .localhost) or an IP address.
func IsLocalOrPrivateDomain(domain string) bool {
	d := strings.ToLower(strings.TrimSpace(domain))
	if net.ParseIP(d) != nil {
		return true
	}
	localSuffixes := []string{
		".local",
		".lan",
		".home",
		".home.arpa",
		".internal",
		".localhost",
		".intranet",
		".priv",
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(d, s) {
			return true
		}
	}
	return false
}

func tlsPolicyID(domain string) string {
	return routeIDPrefix + "tls-" + strings.ReplaceAll(domain, ".", "-")
}

// UpsertRoute creates or replaces a reverse-proxy route for the given domain.
// upstreamDial is e.g. "localhost:3000".
func (c *Client) UpsertRoute(domain, upstreamDial string) error {
	return c.UpsertRouteWithTLS(domain, upstreamDial, "auto")
}

// UpsertRouteWithTLS creates or replaces a reverse-proxy route and configures
// the appropriate TLS automation policy ("auto", "internal", or "http_only").
func (c *Client) UpsertRouteWithTLS(domain, upstreamDial, tlsMode string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	mode := strings.ToLower(strings.TrimSpace(tlsMode))
	if mode == "" {
		mode = "auto"
	}

	serverName, err := c.findOrCreateServer()
	if err != nil {
		return fmt.Errorf("find server: %w", err)
	}

	switch {
	case mode == "http_only":
		// Disable automatic HTTPS & redirects for this hostname so it serves over plain HTTP (:80).
		_ = c.deleteTLSPolicy(domain)
		_ = c.setAutoHTTPSSkip(serverName, domain, true)
	case mode == "internal" || (mode == "auto" && IsLocalOrPrivateDomain(domain)):
		// Configure Caddy's internal CA so it doesn't fail trying public ACME behind CGNAT.
		_ = c.setAutoHTTPSSkip(serverName, domain, false)
		_ = c.ensureInternalTLSPolicy(domain)
	case mode == "auto" && IsTailscaleDomain(domain):
		// Configure Caddy's Tailscale certificate manager for *.ts.net MagicDNS hosts.
		_ = c.setAutoHTTPSSkip(serverName, domain, false)
		_ = c.ensureTailscaleTLSPolicy(domain)
	default:
		// Public ACME ("auto" on a public domain): remove any custom override.
		_ = c.setAutoHTTPSSkip(serverName, domain, false)
		_ = c.deleteTLSPolicy(domain)
	}

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

	return c.postJSON("/config/apps/http/servers/"+serverName+"/routes", body)
}

func (c *Client) ensureInternalTLSPolicy(domain string) error {
	policyID := tlsPolicyID(domain)
	policy := map[string]any{
		"@id":      policyID,
		"subjects": []string{domain},
		"issuers": []map[string]any{
			{"module": "internal"},
		},
	}
	return c.upsertTLSPolicy(policyID, policy)
}

func (c *Client) ensureTailscaleTLSPolicy(domain string) error {
	policyID := tlsPolicyID(domain)
	policy := map[string]any{
		"@id":      policyID,
		"subjects": []string{domain},
		"get_certificate": []map[string]any{
			{"via": "tailscale"},
		},
	}
	return c.upsertTLSPolicy(policyID, policy)
}

func (c *Client) upsertTLSPolicy(policyID string, policy map[string]any) error {
	body, err := json.Marshal(policy)
	if err != nil {
		return err
	}

	if err := c.putID(policyID, body); err == nil {
		return nil
	}

	// Try appending to existing policies array; if /config/apps/tls doesn't exist yet, create it.
	if err := c.postJSON("/config/apps/tls/automation/policies", body); err != nil {
		tlsApp := map[string]any{
			"automation": map[string]any{
				"policies": []any{policy},
			},
		}
		appBody, _ := json.Marshal(tlsApp)
		return c.postJSON("/config/apps/tls", appBody)
	}
	return nil
}

func (c *Client) deleteTLSPolicy(domain string) error {
	policyID := tlsPolicyID(domain)
	req, err := http.NewRequest(http.MethodDelete, c.adminURL+"/id/"+policyID, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// setAutoHTTPSSkip adds or removes a domain from the HTTP server's automatic_https.skip list.
// When skipped, Caddy serves the domain on :80 without provisioning TLS or redirecting HTTP to HTTPS.
func (c *Client) setAutoHTTPSSkip(serverName, domain string, skip bool) error {
	path := "/config/apps/http/servers/" + serverName + "/automatic_https"
	resp, err := c.httpClient.Get(c.adminURL + path)
	var cfg autoHTTPSConfig
	if err == nil {
		if resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&cfg)
		}
		resp.Body.Close()
	}

	var updated []string
	found := false
	for _, s := range cfg.Skip {
		if strings.EqualFold(s, domain) {
			found = true
			if skip {
				updated = append(updated, domain)
			}
		} else {
			updated = append(updated, s)
		}
	}
	if skip && !found {
		updated = append(updated, domain)
	}
	if !skip && !found {
		return nil
	}
	cfg.Skip = updated
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return c.postJSON(path, body)
}

// DeleteRoute removes the reverse-proxy route and any associated TLS policy for the given domain.
func (c *Client) DeleteRoute(domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	_ = c.deleteTLSPolicy(domain)
	if srvName, err := c.findOrCreateServer(); err == nil && srvName != "" {
		_ = c.setAutoHTTPSSkip(srvName, domain, false)
	}

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
