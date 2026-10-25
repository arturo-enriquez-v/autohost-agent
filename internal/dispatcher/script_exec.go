// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"fmt"
	"path/filepath"

	"autohost-agent/internal/domain"
	"autohost-agent/internal/platform/exec"
)

// ScriptExec executes a custom command script received via a job.
// Expected payload keys:
//   - "script_path": absolute path to the .sh file
type ScriptExec struct{}

func (c *ScriptExec) Execute(_ context.Context, payload map[string]any) error {
	scriptPath, ok := payload["script_path"].(string)
	if !ok || scriptPath == "" {
		return fmt.Errorf("missing or invalid script_path in payload")
	}

	// Validate the script name component.
	name := domain.ScriptName(filepath.Base(scriptPath))
	if err := name.Validate(); err != nil {
		return fmt.Errorf("invalid script: %w", err)
	}

	// Validate script containment, symlinks, permissions and owner
	absPath, err := ValidateScriptSecurity(scriptPath)
	if err != nil {
		return fmt.Errorf("script validation failed: %w", err)
	}

	fmt.Printf("🔧 Ejecutando custom command: %s\n", filepath.Base(absPath))
	if err := exec.Exec("bash", absPath); err != nil {
		return fmt.Errorf("custom command failed: %w", err)
	}
	fmt.Printf("✅ Custom command '%s' completado.\n", filepath.Base(absPath))
	return nil
}
