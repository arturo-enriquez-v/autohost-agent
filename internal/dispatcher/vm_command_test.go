// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"strings"
	"testing"
)

func TestParseVMConfig(t *testing.T) {
	payload := map[string]any{
		"name":          "my-test-vm",
		"vm_id":         "vm-1234",
		"vcpus":         float64(4),
		"ram_mb":        float64(2048),
		"disk_gb":       float64(20),
		"image":         "ubuntu-24.04",
		"user":          "admin",
		"ssh_keys":      []any{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB... user@host"},
		"enroll_token":  "token-xyz",
		"cloud_api_url": "https://api.autohost.dev",
	}

	cfg, err := parseVMConfig(payload)
	if err != nil {
		t.Fatalf("unexpected error parsing vm config: %v", err)
	}

	if cfg.Name != "my-test-vm" {
		t.Errorf("expected name 'my-test-vm', got %q", cfg.Name)
	}
	if cfg.VCPUs != 4 {
		t.Errorf("expected 4 vcpus, got %d", cfg.VCPUs)
	}
	if cfg.RAMMB != 2048 {
		t.Errorf("expected 2048 ram_mb, got %d", cfg.RAMMB)
	}
	if cfg.DiskGB != 20 {
		t.Errorf("expected 20 disk_gb, got %d", cfg.DiskGB)
	}
	if cfg.User != "admin" {
		t.Errorf("expected user 'admin', got %q", cfg.User)
	}
	if len(cfg.SSHKeys) != 1 {
		t.Errorf("expected 1 ssh key, got %d", len(cfg.SSHKeys))
	}
}

func TestParseVMConfig_InvalidName(t *testing.T) {
	invalidNames := []string{
		"invalid/name",
		"invalid name",
		"; rm -rf /",
		"",
	}

	for _, name := range invalidNames {
		_, err := parseVMConfig(map[string]any{"name": name})
		if err == nil {
			t.Errorf("expected error for invalid VM name %q, got nil", name)
		}
	}
}

func TestExecVMCommand_Invalid(t *testing.T) {
	ctx := context.Background()

	res := execVMCommand(ctx, "vm.unknown.action.extra", nil)
	if res.Err == nil {
		t.Errorf("expected error for invalid command pattern, got nil")
	}

	res = execVMCommand(ctx, "vm.start.bad;name", nil)
	if res.Err == nil {
		t.Errorf("expected error for bad VM name, got nil")
	}

	res = execVMCommand(ctx, "vm.invalid_action.vm1", nil)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "unsupported vm action") {
		t.Errorf("expected unsupported action error, got %v", res.Err)
	}
}
