// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package exec

import (
	"bytes"
	"os"
	"os/exec"
)

func Exec(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// ExecWithOutput runs a command and returns the combined stdout+stderr output.
func ExecWithOutput(cmd string, args ...string) (string, error) {
	c := exec.Command(cmd, args...)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	return buf.String(), err
}

func ExecShell(script string) error {
	return Exec("bash", "-eo", "pipefail", "-c", script)
}

func ExecWithDir(dir string, cmdName string, args ...string) error {
	cmd := exec.Command(cmdName, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ExecWithDirOutput runs a command in the given directory and returns the
// combined stdout+stderr output together with any error.
func ExecWithDirOutput(dir string, cmdName string, args ...string) (string, error) {
	cmd := exec.Command(cmdName, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
