// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package httpclient

// API endpoint constants.
const (
	EndpointHeartbeat     = "/v1/heartbeats/heartbeat"
	EndpointVersionReport = "/v1/heartbeats/version"
	EndpointMetrics       = "/v1/node-metrics/metrics"
	EndpointRefreshToken  = "/v1/auth/refresh"
	EndpointNetworkStats  = "/v1/agent/network-stats"
	EndpointRenewCert     = "/v1/enrollments/renew-cert"
)
