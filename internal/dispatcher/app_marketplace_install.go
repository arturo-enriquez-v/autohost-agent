// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"autohost-agent/internal/adapters/docker"
	"autohost-agent/internal/domain"
	"autohost-agent/internal/platform/paths"
)

// MarketplaceInstall installs an app from a compose template and env vars
// received from the cloud backend (downloaded from R2).
type MarketplaceInstall struct{}

// marketplaceInstallParams mirrors the JSON sent by the cloud-api.
type marketplaceInstallParams struct {
	AppName string            `json:"app_name"`
	Compose string            `json:"compose"`
	Env     map[string]string `json:"env"`
}

func (c *MarketplaceInstall) Execute(_ context.Context, payload map[string]any) error {
	// The payload must contain "params" as a JSON string.
	raw, ok := payload["params"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("marketplace.install: missing params in payload")
	}

	var p marketplaceInstallParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("marketplace.install: decode params: %w", err)
	}
	if p.AppName == "" || p.Compose == "" {
		return fmt.Errorf("marketplace.install: app_name and compose are required")
	}

	appName := domain.AppName(p.AppName)
	if err := appName.Validate(); err != nil {
		return fmt.Errorf("marketplace.install: invalid app_name: %w", err)
	}

	appDir := filepath.Join(paths.GetSubdir("apps"), p.AppName)

	// Validate env var keys to prevent malformed .env or injection attacks
	if err := validateEnvKeys(p.Env); err != nil {
		return fmt.Errorf("marketplace.install: %w", err)
	}

	// Substitute $variable placeholders in the compose template.
	composeContent := substituteVars(p.Compose, p.Env)

	// Perform strict security validation on compose.yml before writing to disk
	if err := docker.ValidateCompose(composeContent, appDir); err != nil {
		return fmt.Errorf("marketplace.install: security validation failed: %w", err)
	}

	if err := os.MkdirAll(appDir, 0755); err != nil {
		return fmt.Errorf("marketplace.install: create app dir %s: %w", appDir, err)
	}

	composePath := filepath.Join(appDir, "compose.yml")
	if err := os.WriteFile(composePath, []byte(composeContent), 0640); err != nil {
		return fmt.Errorf("marketplace.install: write compose.yml: %w", err)
	}

	// Write .env file alongside compose.yml with restricted permissions.
	envContent := buildEnvFile(p.Env)
	envPath := filepath.Join(appDir, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		return fmt.Errorf("marketplace.install: write .env: %w", err)
	}

	fmt.Printf("🚀 Installing marketplace app '%s' from %s\n", p.AppName, appDir)

	if err := docker.Start(appName); err != nil {
		return fmt.Errorf("marketplace.install: docker compose up: %w", err)
	}

	fmt.Printf("✅ App '%s' installed and running.\n", p.AppName)
	return nil
}

// validateEnvKeys ensures environment variable keys are valid identifiers.
func validateEnvKeys(env map[string]string) error {
	for k := range env {
		if k == "" {
			return fmt.Errorf("empty environment variable key")
		}
		for i, r := range k {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
				return fmt.Errorf("invalid environment variable key %q: only alphanumeric and underscore allowed", k)
			}
			if i == 0 && (r >= '0' && r <= '9') {
				return fmt.Errorf("invalid environment variable key %q: cannot start with digit", k)
			}
		}
	}
	return nil
}

// substituteVars replaces $key placeholders in the template with values from
// the env map. Keys are processed longest-first to avoid partial matches.
func substituteVars(template string, env map[string]string) string {
	// Sort keys by descending length so longer keys are substituted first.
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})

	result := template
	for _, k := range keys {
		result = strings.ReplaceAll(result, "$"+k, env[k])
	}
	return result
}

// buildEnvFile produces a KEY=VALUE .env file content from the map.
func buildEnvFile(env map[string]string) string {
	var sb strings.Builder
	for k, v := range env {
		sb.WriteString(k)
		sb.WriteByte('=')
		// Double-quote if value contains spaces, quotes or special chars
		if strings.ContainsAny(v, " \t\n\"'\\$`") {
			escaped := strings.ReplaceAll(v, "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
			sb.WriteString(fmt.Sprintf("%q", escaped))
		} else {
			sb.WriteString(v)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
