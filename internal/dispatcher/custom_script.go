// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"autohost-agent/internal/domain"
	"autohost-agent/internal/platform/exec"
)

// CustomScriptCommand executes a known .sh file at a fixed path, capturing
// its combined stdout+stderr output and returning it.
type CustomScriptCommand struct {
	ScriptPath string
}

// Execute satisfies the Command interface; the Registry calls ExecuteWithOutput
// directly for CustomScriptCommand instances.
func (c *CustomScriptCommand) Execute(ctx context.Context, _ map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx)
	return err
}

// ExecuteWithOutput runs the script and returns (output, error).
func (c *CustomScriptCommand) ExecuteWithOutput(_ context.Context) (string, error) {
	absPath, err := ValidateScriptSecurity(c.ScriptPath)
	if err != nil {
		return "", fmt.Errorf("script validation failed: %w", err)
	}
	return exec.ExecWithOutput("bash", absPath)
}

// RegisterCustomScripts discovers all .sh files in the given directory and
// registers each one as a CustomScriptCommand in the registry.
// The handler key is the filename without the .sh extension.
func RegisterCustomScripts(r *Registry, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no custom commands yet — fine
		}
		return fmt.Errorf("read custom commands dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".sh" {
			continue
		}
		cmdName := name[:len(name)-len(".sh")]
		if err := domain.ScriptName(name).Validate(); err != nil {
			continue // skip invalid names
		}
		scriptPath := filepath.Join(dir, name)
		r.RegisterCustom(cmdName, &CustomScriptCommand{ScriptPath: scriptPath})
	}
	return nil
}
