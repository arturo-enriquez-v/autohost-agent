// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package sysinfo

import (
	"bufio"
	"os"
	"strings"
)

type OsRelease struct {
	ID     string
	IDLike string
}

func ReadOSRelease() OsRelease {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return OsRelease{}
	}
	defer f.Close()
	kv := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		k := parts[0]
		v := strings.Trim(parts[1], `"'`)
		kv[k] = v
	}
	return OsRelease{ID: kv["ID"], IDLike: kv["ID_LIKE"]}
}
