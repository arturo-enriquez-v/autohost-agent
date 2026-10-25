// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"fmt"

	"autohost-agent/internal/adapters/docker"
)

// AppRemove removes a docker compose application and its files.
type AppRemove struct{}

func (c *AppRemove) Execute(_ context.Context, payload map[string]any) error {
	appName, err := extractAppName(payload)
	if err != nil {
		return err
	}
	fmt.Printf("🗑️  Eliminando aplicación '%s'...\n", appName)
	if err := docker.Remove(appName); err != nil {
		return err
	}
	fmt.Printf("✅ Aplicación '%s' eliminada.\n", appName)
	return nil
}
