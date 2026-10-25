// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"autohost-agent/internal/adapters/caddy"
	"autohost-agent/internal/security"
)

// caddyClient is a package-level default Caddy client using the standard admin URL.
// Commands read CADDY_ADMIN_URL from env via the client constructor default.
var caddyClient = caddy.NewClient("")

// forbiddenUpstreamPorts lists sensitive administrative and database ports that
// must never be exposed to public reverse proxy routes.
var forbiddenUpstreamPorts = map[int]string{
	2019:  "Caddy Admin API",
	22:    "SSH",
	2375:  "Docker Daemon (Plaintext)",
	2376:  "Docker Daemon (TLS)",
	5432:  "PostgreSQL",
	6379:  "Redis",
	3306:  "MySQL",
	27017: "MongoDB",
	2379:  "etcd Client",
	2380:  "etcd Peer",
	10250: "Kubelet API",
	9090:  "gRPC / Prometheus",
	8500:  "Consul API",
}

// CaddyUpsertRoute adds or replaces a reverse-proxy route in Caddy.
// Expected payload keys:
//   - "domain"   string — e.g. "myapp.example.com"
//   - "upstream" string — e.g. "localhost:3000"
type CaddyUpsertRoute struct{}

func (c *CaddyUpsertRoute) Execute(_ context.Context, payload map[string]any) error {
	domain, ok := payload["domain"].(string)
	if !ok || domain == "" {
		return fmt.Errorf("caddy.upsert-route: missing domain")
	}
	upstream, ok := payload["upstream"].(string)
	if !ok || upstream == "" {
		return fmt.Errorf("caddy.upsert-route: missing upstream")
	}

	if err := validateDomain(domain); err != nil {
		return fmt.Errorf("caddy.upsert-route: invalid domain: %w", err)
	}

	if err := validateUpstream(upstream); err != nil {
		return fmt.Errorf("caddy.upsert-route: invalid upstream: %w", err)
	}

	if !caddyClient.IsRunning() {
		return fmt.Errorf("caddy.upsert-route: Caddy admin API not reachable at localhost:2019")
	}

	if err := caddyClient.UpsertRoute(domain, upstream); err != nil {
		return fmt.Errorf("caddy.upsert-route: %w", err)
	}
	fmt.Printf("✅ Caddy: route %s → %s configured\n", domain, upstream)
	return nil
}

// validateDomain ensures domain is a valid FQDN and not localhost or raw IP.
func validateDomain(domain string) error {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return errors.New("domain is required")
	}
	if domain == "localhost" || strings.HasPrefix(domain, "localhost:") || strings.HasSuffix(domain, ".localhost") {
		return errors.New("domain cannot be localhost")
	}
	if ip := net.ParseIP(domain); ip != nil {
		return errors.New("domain cannot be an IP address literal")
	}
	if len(domain) > 253 {
		return errors.New("domain exceeds maximum length of 253 characters")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return errors.New("domain must have at least two labels (e.g. app.example.com)")
	}
	for _, part := range labels {
		if len(part) == 0 || len(part) > 63 {
			return fmt.Errorf("invalid domain label %q", part)
		}
		for i, r := range part {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("invalid character %q in domain label", r)
			}
			if (i == 0 || i == len(part)-1) && r == '-' {
				return fmt.Errorf("domain label %q cannot start or end with hyphen", part)
			}
		}
	}
	return nil
}

// validateUpstream ensures upstream is a valid host:port and does not target protected ports.
func validateUpstream(upstream string) error {
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return errors.New("upstream is required")
	}

	host, portStr, err := net.SplitHostPort(upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream %q: must be in host:port format", upstream)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid upstream port %q: must be between 1 and 65535", portStr)
	}

	if serviceName, forbidden := forbiddenUpstreamPorts[port]; forbidden {
		return fmt.Errorf("security violation: upstream port %d is reserved for %s and cannot be exposed via Caddy reverse proxy", port, serviceName)
	}

	// Check if host is dangerous IP (e.g. metadata 169.254.169.254)
	if ip := net.ParseIP(host); ip != nil {
		if security.IsDangerousIP(ip) {
			return fmt.Errorf("security violation: upstream host %s is in a forbidden IP range", host)
		}
	}

	return nil
}

// CaddyDeleteRoute removes a reverse-proxy route from Caddy.
// Expected payload keys:
//   - "domain" string
type CaddyDeleteRoute struct{}

func (c *CaddyDeleteRoute) Execute(_ context.Context, payload map[string]any) error {
	domain, ok := payload["domain"].(string)
	if !ok || domain == "" {
		return fmt.Errorf("caddy.delete-route: missing domain")
	}

	if err := validateDomain(domain); err != nil {
		return fmt.Errorf("caddy.delete-route: invalid domain: %w", err)
	}

	if !caddyClient.IsRunning() {
		return fmt.Errorf("caddy.delete-route: Caddy admin API not reachable at localhost:2019")
	}

	if err := caddyClient.DeleteRoute(domain); err != nil {
		return fmt.Errorf("caddy.delete-route: %w", err)
	}
	fmt.Printf("🗑️  Caddy: route %s removed\n", domain)
	return nil
}

// CaddyStatus reports whether Caddy is running and lists current routes.
type CaddyStatus struct{}

func (c *CaddyStatus) Execute(_ context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(context.Background(), payload)
	return err
}

func (c *CaddyStatus) ExecuteWithOutput(_ context.Context, _ map[string]any) (string, error) {
	if !caddyClient.IsRunning() {
		return `{"caddy_running":false}`, nil
	}

	routes, err := caddyClient.ListAllRoutes()
	if err != nil {
		return "", fmt.Errorf("caddy.status: %w", err)
	}

	out, _ := json.MarshalIndent(map[string]any{
		"caddy_running": true,
		"routes":        routes,
	}, "", "  ")
	return string(out), nil
}
