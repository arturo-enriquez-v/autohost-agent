// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package api

import (
	"autohost-agent/internal/domain"
	"context"
)

// SendMetrics sends node metrics to the API.
func (c *Client) SendMetrics(ctx context.Context, metrics *domain.Metrics) error {
	return c.post(ctx, EndpointMetrics, metrics)
}
