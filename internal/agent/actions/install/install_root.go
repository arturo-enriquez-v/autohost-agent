// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package install

import (
	"autohost-agent/internal/agent/actions/assets"
	"autohost-agent/pkg/dir"
	"autohost-agent/pkg/file"
)

func InstallAction(appName string, envData []byte) error {

	path := dir.GetRootAppDir()

	dataFile, err := assets.ReadCompose(appName)
	if err != nil {
		return err
	}

	file.WriteFile(path, "compose.yml", dataFile)
	file.WriteFile(path, ".env", envData)

	return nil
}
