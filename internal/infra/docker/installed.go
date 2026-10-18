// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package docker

import "os/exec"

func DockerInstalled() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}
