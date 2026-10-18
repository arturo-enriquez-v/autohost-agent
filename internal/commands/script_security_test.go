// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"autohost-agent/internal/domain"
)

func TestValidateScriptSecurity_ValidScript(t *testing.T) {
	tempDir := t.TempDir()
	origDir := domain.CustomCommandsDir
	domain.CustomCommandsDir = tempDir
	defer func() { domain.CustomCommandsDir = origDir }()

	scriptPath := filepath.Join(tempDir, "hello.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho 'hello'\n"), 0750); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	validated, err := ValidateScriptSecurity(scriptPath)
	if err != nil {
		t.Fatalf("expected valid script to pass, got: %v", err)
	}
	if validated != scriptPath {
		t.Errorf("expected %s, got %s", scriptPath, validated)
	}
}

func TestValidateScriptSecurity_RejectsWorldWritable(t *testing.T) {
	tempDir := t.TempDir()
	origDir := domain.CustomCommandsDir
	domain.CustomCommandsDir = tempDir
	defer func() { domain.CustomCommandsDir = origDir }()

	scriptPath := filepath.Join(tempDir, "insecure.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho 'insecure'\n"), 0750); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}
	if err := os.Chmod(scriptPath, 0777); err != nil {
		t.Fatalf("failed to chmod script: %v", err)
	}

	_, err := ValidateScriptSecurity(scriptPath)
	if err == nil {
		t.Fatalf("expected error for world-writable script, got nil")
	}
	if !errors.Is(err, ErrInsecureScriptPermissions) {
		t.Errorf("expected ErrInsecureScriptPermissions, got: %v", err)
	}
}

func TestValidateScriptSecurity_RejectsSymlinkEscape(t *testing.T) {
	tempDir := t.TempDir()
	origDir := domain.CustomCommandsDir
	commandsDir := filepath.Join(tempDir, "commands")
	secretDir := filepath.Join(tempDir, "secret")
	_ = os.MkdirAll(commandsDir, 0750)
	_ = os.MkdirAll(secretDir, 0750)

	domain.CustomCommandsDir = commandsDir
	defer func() { domain.CustomCommandsDir = origDir }()

	targetScript := filepath.Join(secretDir, "evil.sh")
	if err := os.WriteFile(targetScript, []byte("#!/bin/bash\necho 'evil'\n"), 0750); err != nil {
		t.Fatalf("failed to write target script: %v", err)
	}

	symlinkPath := filepath.Join(commandsDir, "link.sh")
	if err := os.Symlink(targetScript, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	_, err := ValidateScriptSecurity(symlinkPath)
	if err == nil {
		t.Fatalf("expected error for escaping symlink, got nil")
	}
}
