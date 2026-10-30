// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateTokensInConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	initialYAML := `# Autohost Agent Configuration
api_url: "https://api.autohost.cloud"
grpc_address: "grpc.autohost.cloud:443"
agent_token: "old-access-token"
refresh_token: "old-refresh-token"
node_id: "node-12345"
tags:
  - "production"
  - "us-east"
grpc_insecure: false
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0640); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	newAccess := "new-jwt-access-token-xyz"
	newRefresh := "new-refresh-token-abc"

	if err := updateTokensInConfigFile(configPath, newAccess, newRefresh); err != nil {
		t.Fatalf("updateTokensInConfigFile failed: %v", err)
	}

	// Verify updated content
	updatedBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read updated config: %v", err)
	}
	updatedStr := string(updatedBytes)

	if !strings.Contains(updatedStr, `agent_token: "new-jwt-access-token-xyz"`) {
		t.Errorf("expected updated agent_token, got:\n%s", updatedStr)
	}
	if !strings.Contains(updatedStr, `refresh_token: "new-refresh-token-abc"`) {
		t.Errorf("expected updated refresh_token, got:\n%s", updatedStr)
	}
	if !strings.Contains(updatedStr, `node_id: node-12345`) && !strings.Contains(updatedStr, `node_id: "node-12345"`) {
		t.Errorf("expected node_id to be preserved, got:\n%s", updatedStr)
	}
	if !strings.Contains(updatedStr, "production") {
		t.Errorf("expected tags to be preserved, got:\n%s", updatedStr)
	}

	// Check file permissions
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0640 && perm != 0600 {
		t.Errorf("expected permission 0640 or 0600, got: %o", perm)
	}
}

func TestUpdateTokensInConfigFile_NewKeysAppended(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "minimal_config.yaml")

	initialYAML := `api_url: "https://api.autohost.cloud"
node_id: "node-999"
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0640); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	newAccess := "token-1"
	newRefresh := "token-2"

	if err := updateTokensInConfigFile(configPath, newAccess, newRefresh); err != nil {
		t.Fatalf("updateTokensInConfigFile failed: %v", err)
	}

	updatedBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read updated config: %v", err)
	}
	updatedStr := string(updatedBytes)

	if !strings.Contains(updatedStr, `agent_token: "token-1"`) {
		t.Errorf("expected agent_token to be appended, got:\n%s", updatedStr)
	}
	if !strings.Contains(updatedStr, `refresh_token: "token-2"`) {
		t.Errorf("expected refresh_token to be appended, got:\n%s", updatedStr)
	}
}
