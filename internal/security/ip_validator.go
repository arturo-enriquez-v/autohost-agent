// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

var (
	// ErrDangerousDestination is returned when an address targets a dangerous or forbidden network.
	ErrDangerousDestination = errors.New("security violation: destination IP is in a forbidden range (cloud metadata, link-local, multicast, or reserved)")
)

// forbiddenCIDRs contains IP ranges that must never be accessed by health probes.
var forbiddenCIDRs = []*net.IPNet{
	// IPv4 Link-Local & Cloud Metadata (169.254.0.0/16)
	mustParseCIDR("169.254.0.0/16"),
	// IPv6 Link-Local (fe80::/10)
	mustParseCIDR("fe80::/10"),
	// IPv4 "This host on this network" (0.0.0.0/8)
	mustParseCIDR("0.0.0.0/8"),
	// IPv4 Multicast (224.0.0.0/4)
	mustParseCIDR("224.0.0.0/4"),
	// IPv6 Multicast (ff00::/8)
	mustParseCIDR("ff00::/8"),
	// IPv4 Reserved for future use (240.0.0.0/4)
	mustParseCIDR("240.0.0.0/4"),
	// IPv4 Carrier-Grade NAT (100.64.0.0/10)
	mustParseCIDR("100.64.0.0/10"),
}

// dangerousHostnames contains known cloud metadata domain names.
var dangerousHostnames = map[string]bool{
	"metadata.google.internal": true,
	"instance-data":            true,
	"metadata.internal":        true,
	"169.254.169.254":          true,
}

func mustParseCIDR(s string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %s: %v", s, err))
	}
	return ipnet
}

// IsDangerousIP checks if an IP belongs to a blocked or dangerous range (metadata, link-local, multicast).
func IsDangerousIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Reject unspecified (0.0.0.0 / ::)
	if ip.IsUnspecified() {
		return true
	}

	// Reject multicast
	if ip.IsMulticast() {
		return true
	}

	// Check forbidden CIDRs
	for _, block := range forbiddenCIDRs {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// ResolveAndValidateIPs resolves host via DNS and validates that ALL resolved IP addresses are safe.
// If any IP is dangerous, it aborts immediately to prevent DNS Rebinding / Split-Horizon attacks.
func ResolveAndValidateIPs(ctx context.Context, host string) ([]net.IP, error) {
	cleanHost := strings.TrimSpace(strings.ToLower(host))
	if cleanHost == "" {
		return nil, errors.New("empty host")
	}

	// Strip IPv6 brackets if present (e.g. "[::1]")
	cleanHost = strings.TrimPrefix(cleanHost, "[")
	cleanHost = strings.TrimSuffix(cleanHost, "]")

	// Check dangerous hostnames
	if dangerousHostnames[cleanHost] {
		return nil, fmt.Errorf("%w: hostname %q is a blocked cloud metadata endpoint", ErrDangerousDestination, host)
	}

	// If host is a direct IP literal
	if ip := net.ParseIP(cleanHost); ip != nil {
		if IsDangerousIP(ip) {
			return nil, fmt.Errorf("%w: IP %s is forbidden", ErrDangerousDestination, ip.String())
		}
		return []net.IP{ip}, nil
	}

	// Resolve host via DNS
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, cleanHost)
	if err != nil {
		return nil, fmt.Errorf("dns lookup for %s failed: %w", host, err)
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("no IP addresses found for %s", host)
	}

	safeIPs := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if IsDangerousIP(addr.IP) {
			return nil, fmt.Errorf("%w: resolved IP %s for host %s is forbidden", ErrDangerousDestination, addr.IP.String(), host)
		}
		safeIPs = append(safeIPs, addr.IP)
	}

	return safeIPs, nil
}
