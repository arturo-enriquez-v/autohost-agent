// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package file

import (
	"os"
)

func WriteFile(path string, filename string, data []byte) error {

	pathFile := path + "/" + filename

	err := os.WriteFile(pathFile, data, 0644)
	if err != nil {
		return err
	}

	return nil
}
