// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package api

import (
	"autohost-agent/pkg/sysinfo"
	"context"
)

// NetworkStatsPayload is the JSON body sent to the API network-stats endpoint.
type NetworkStatsPayload struct {
	Interfaces  []sysinfo.NetworkInterfaceStats `json:"interfaces"`
	Ports       []sysinfo.ListeningPort         `json:"ports"`
	Peers       []sysinfo.VPNPeer               `json:"peers"`
	Connections []sysinfo.ActiveConnection      `json:"connections"`
	Requests    []sysinfo.HTTPRequestLog        `json:"requests"`
	RPS         float64                         `json:"rps"`
}

// SendNetworkStats posts current network stats, listening ports, VPN peers, connections, and HTTP requests to the API.
func (c *Client) SendNetworkStats(ctx context.Context, stats *NetworkStatsPayload) error {
	return c.post(ctx, EndpointNetworkStats, stats)
}
