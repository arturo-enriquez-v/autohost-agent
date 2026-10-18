// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package assets

import (
	"embed"
	"io/fs"
	"path/filepath"
)

//go:embed docker/**/*
var dockerFS embed.FS

// ReadCompose returns the compose.yml template for the given app.
func ReadCompose(app string) ([]byte, error) {
	return fs.ReadFile(dockerFS, filepath.Join("docker", app, "compose.yml"))
}

// ReadEnvExample returns the .env.example template for the given app.
func ReadEnvExample(app string) ([]byte, error) {
	return fs.ReadFile(dockerFS, filepath.Join("docker", app, ".env.example"))
}
