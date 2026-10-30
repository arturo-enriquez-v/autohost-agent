// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package httpclient

import (
	"context"

	"autohost-agent/internal/domain"
)

// RegisterCustomCommand sends a custom command registration to the API.
func (c *Client) RegisterCustomCommand(ctx context.Context, cmd domain.CustomCommand) error {
	return c.post(ctx, EndpointCustomCommands, cmd)
}
