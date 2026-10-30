// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package domain

import (
	"fmt"
	"regexp"
)

// CustomCommandsDir is the default directory where custom command scripts are stored.
var CustomCommandsDir = "/var/lib/autohost/commands"

// ScriptName represents the filename of a custom command script.
type ScriptName string

// Validate ensures the script name is safe (alphanumeric, hyphens, underscores, ending in .sh).
func (s ScriptName) Validate() error {
	if s == "" {
		return fmt.Errorf("script name cannot be empty")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+\.sh$`).MatchString(string(s)) {
		return fmt.Errorf("invalid script name: must be alphanumeric with hyphens/underscores and end in .sh")
	}
	return nil
}

// CustomCommand represents a registered custom command script.
type CustomCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ScriptPath  string `json:"script_path"`
	NodeID      string `json:"node_id"`
}
