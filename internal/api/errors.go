// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package api

import (
	"fmt"
	"net/http"
)

// ErrStatus represents an HTTP error response from the API.
type ErrStatus struct {
	Code int
}

func (e *ErrStatus) Error() string {
	return fmt.Sprintf("api: %d %s", e.Code, http.StatusText(e.Code))
}
