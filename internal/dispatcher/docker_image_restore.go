// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DockerImageRestore implements CommandWithOutput to restore a Docker image from a compressed tar archive.
type DockerImageRestore struct{}

type ImageRestorePayload struct {
	ContainerName     string   `json:"container_name"`
	LocalPath         string   `json:"local_path"`
	ParentDownloadURL string   `json:"parent_download_url"`
	NodeToken         string   `json:"node_token"`
	S3                S3Config `json:"s3"`
}

type ImageRestoreResult struct {
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

func (c *DockerImageRestore) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *DockerImageRestore) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	startTime := time.Now()

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	var req ImageRestorePayload
	if err := json.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to parse image restore payload: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "autohost-imgrestore-*.image.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpFilePath := tmpFile.Name()
	defer os.Remove(tmpFilePath)
	_ = tmpFile.Close()

	if err := fetchBackupFile(ctx, req.LocalPath, req.ParentDownloadURL, req.NodeToken, req.S3, tmpFilePath); err != nil {
		return "", fmt.Errorf("fetch image backup failed: %w", err)
	}

	// Read compressed file and pipe gunzipped tar to `docker load`
	f, err := os.Open(tmpFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to open downloaded image file: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader for image: %w", err)
	}
	defer gr.Close()

	cmd := exec.CommandContext(ctx, "docker", "load")
	cmd.Stdin = gr
	var errBuf bytes.Buffer
	var outBuf bytes.Buffer
	cmd.Stderr = &errBuf
	cmd.Stdout = &outBuf

	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		if stderr != "" {
			return "", fmt.Errorf("docker load failed: %s (%w)", stderr, err)
		}
		return "", fmt.Errorf("docker load failed: %w", err)
	}

	duration := time.Since(startTime).Milliseconds()
	result := ImageRestoreResult{
		Status:     "completed",
		DurationMS: duration,
	}

	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

