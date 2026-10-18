// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMarketplaceInstall_RejectsPrivileged(t *testing.T) {
	cmd := &MarketplaceInstall{}
	params := marketplaceInstallParams{
		AppName: "evilapp",
		Compose: `
services:
  app:
    image: alpine
    privileged: true
`,
		Env: map[string]string{"PORT": "8080"},
	}
	raw, _ := json.Marshal(params)

	err := cmd.Execute(context.Background(), map[string]any{
		"params": string(raw),
	})

	if err == nil {
		t.Fatalf("expected error rejecting privileged compose, got nil")
	}
}

func TestMarketplaceInstall_RejectsSensitiveVolumeMount(t *testing.T) {
	cmd := &MarketplaceInstall{}
	params := marketplaceInstallParams{
		AppName: "evilapp",
		Compose: `
services:
  app:
    image: alpine
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
`,
		Env: map[string]string{"PORT": "8080"},
	}
	raw, _ := json.Marshal(params)

	err := cmd.Execute(context.Background(), map[string]any{
		"params": string(raw),
	})

	if err == nil {
		t.Fatalf("expected error rejecting docker.sock mount, got nil")
	}
}

func TestMarketplaceInstall_RejectsInvalidEnvKey(t *testing.T) {
	cmd := &MarketplaceInstall{}
	params := marketplaceInstallParams{
		AppName: "testapp",
		Compose: `
services:
  app:
    image: nginx
`,
		Env: map[string]string{"INVALID=KEY\nEVIL": "val"},
	}
	raw, _ := json.Marshal(params)

	err := cmd.Execute(context.Background(), map[string]any{
		"params": string(raw),
	})

	if err == nil {
		t.Fatalf("expected error rejecting invalid env key, got nil")
	}
}
