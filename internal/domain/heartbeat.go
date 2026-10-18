// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package domain

// HeartbeatPayload represents the data sent in each heartbeat to the API.
type HeartbeatPayload struct {
	NodeID        string   `json:"node_id"`
	Hostname      string   `json:"hostname"`
	Tags          []string `json:"tags"`
	OS            string   `json:"os"`
	UptimeSeconds uint64   `json:"uptime_seconds"`
}
