// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// VPNPeer represents an active WireGuard/Tailscale/Headscale peer discovered on the host.
type VPNPeer struct {
	IP        string  `json:"ip"`
	Hostname  string  `json:"hostname"`
	Online    bool    `json:"online"`
	LatencyMs float64 `json:"latency_ms"`
	Relay     string  `json:"relay,omitempty"` // "direct" or relay region e.g. "lax"
}

// tailscaleStatusPartial maps fields from `tailscale status --json`.
type tailscaleStatusPartial struct {
	Self struct {
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	Peer map[string]struct {
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
		Active       bool     `json:"Active"`
		Relay        string   `json:"Relay"`
		LastHandshake string  `json:"LastHandshake"`
	} `json:"Peer"`
}

// GetVPNPeers executes `tailscale status --json` with a 3-second timeout.
// Returns a list of active VPN peers with their IPs, online state, and relay mode.
// If tailscale CLI is not installed or fails, returns an empty slice gracefully.
func GetVPNPeers() ([]VPNPeer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		// Try headscale nodes list fallback if available
		return nil, nil
	}

	var status tailscaleStatusPartial
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, err
	}

	var peers []VPNPeer
	for _, p := range status.Peer {
		if len(p.TailscaleIPs) == 0 {
			continue
		}

		ip := p.TailscaleIPs[0]
		hostname := p.HostName
		if hostname == "" {
			hostname = strings.Split(p.DNSName, ".")[0]
		}

		relayMode := "direct"
		if p.Relay != "" && p.Relay != "direct" {
			relayMode = p.Relay
		}

		peers = append(peers, VPNPeer{
			IP:        ip,
			Hostname:  hostname,
			Online:    p.Online || p.Active,
			LatencyMs: 0, // Ping latency if available
			Relay:     relayMode,
		})
	}

	return peers, nil
}
