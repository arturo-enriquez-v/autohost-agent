// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"fmt"

	"autohost-agent/internal/adapters/docker"
)

// AppRestart restarts a docker compose application (stop + start).
type AppRestart struct{}

func (c *AppRestart) Execute(_ context.Context, payload map[string]any) error {
	appName, err := extractAppName(payload)
	if err != nil {
		return err
	}
	fmt.Printf("🔄 Reiniciando aplicación '%s'...\n", appName)
	if err := docker.Stop(appName); err != nil {
		return err
	}
	if err := docker.Start(appName); err != nil {
		return err
	}
	fmt.Printf("✅ Aplicación '%s' reiniciada.\n", appName)
	return nil
}
