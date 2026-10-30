// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package fs

import (
	"os"
	"path/filepath"
)

func WriteFile(dir string, filename string, data []byte) error {
	filePath := filepath.Join(dir, filename)
	return os.WriteFile(filePath, data, 0644)
}
