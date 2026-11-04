//go:build !linux

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"errors"
	"os"
	"os/exec"
)

func openPTY(cols, rows uint32) (*os.File, *os.File, error) {
	return nil, nil, errors.New("interactive terminal PTY is only supported on Linux nodes")
}

func setPTYWindowSize(ptmx *os.File, cols, rows uint32) {
}

func configureCmdPTY(cmd *exec.Cmd, tty *os.File) {
}

func killProcessGroup(pid int) {
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
	}
}
