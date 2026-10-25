// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"context"
	"fmt"

	"autohost-agent/internal/domain"
	"autohost-agent/internal/adapters/docker"
)

// AppStart starts a docker compose application.
type AppStart struct{}

func (c *AppStart) Execute(_ context.Context, payload map[string]any) error {
	appName, err := extractAppName(payload)
	if err != nil {
		return err
	}
	fmt.Printf("🔄 Iniciando aplicación '%s'...\n", appName)
	if err := docker.Start(appName); err != nil {
		return err
	}
	fmt.Printf("✅ Aplicación '%s' iniciada.\n", appName)
	return nil
}

// extractAppName extracts and validates the app_name from a job payload.
func extractAppName(payload map[string]any) (domain.AppName, error) {
	raw, ok := payload["app_name"].(string)
	if !ok || raw == "" {
		return "", fmt.Errorf("missing or invalid app_name in payload")
	}
	name := domain.AppName(raw)
	if err := name.Validate(); err != nil {
		return "", err
	}
	return name, nil
}
