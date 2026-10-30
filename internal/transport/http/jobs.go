// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package httpclient

import (
	"context"
	"time"
)

// JobResult represents the outcome of a job execution sent back to the API.
type JobResult struct {
	JobID       string `json:"job_id"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	CompletedAt int64  `json:"completed_at"`
}

// ReportJobResult sends a job execution result to the API.
func (c *Client) ReportJobResult(ctx context.Context, jobID, status, errMsg string) error {
	result := JobResult{
		JobID:       jobID,
		Status:      status,
		Error:       errMsg,
		CompletedAt: time.Now().Unix(),
	}
	return c.post(ctx, EndpointJobResult, result)
}
