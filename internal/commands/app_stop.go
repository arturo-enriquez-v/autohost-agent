// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"context"
	"fmt"

	"autohost-agent/internal/adapters/docker"
)

// AppStop stops a docker compose application.
type AppStop struct{}

func (c *AppStop) Execute(_ context.Context, payload map[string]any) error {
	appName, err := extractAppName(payload)
	if err != nil {
		return err
	}
	fmt.Printf("🛑 Deteniendo aplicación '%s'...\n", appName)
	if err := docker.Stop(appName); err != nil {
		return err
	}
	fmt.Printf("✅ Aplicación '%s' detenida.\n", appName)
	return nil
}
