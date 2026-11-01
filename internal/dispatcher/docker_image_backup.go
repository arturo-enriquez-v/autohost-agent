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

	"autohost-agent/internal/adapters/docker"
)

// DockerImageBackup backs up a container's Docker image using docker save + gzip compression.
type DockerImageBackup struct{}

type ImageBackupPayload struct {
	ContainerName      string   `json:"container_name"`
	BackupRecordID     string   `json:"backup_record_id"`
	StorageDestination string   `json:"storage_destination"`
	RetentionCount     int      `json:"retention_count"`
	APIURL             string   `json:"api_url"`
	NodeToken          string   `json:"node_token"`
	S3                 S3Config `json:"s3"`
}

func (c *DockerImageBackup) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *DockerImageBackup) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	startTime := time.Now()

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	var req ImageBackupPayload
	if err := json.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to parse image backup payload: %w", err)
	}

	if req.ContainerName == "" {
		return "", fmt.Errorf("container_name is required")
	}
	if !safeContainerName.MatchString(req.ContainerName) {
		return "", fmt.Errorf("invalid container name: %q", req.ContainerName)
	}
	if req.StorageDestination == "s3" {
		if req.S3.Bucket == "" || req.S3.Endpoint == "" || req.S3.AccessKeyID == "" {
			return "", fmt.Errorf("invalid S3/R2 storage configuration")
		}
	}

	// 1. Inspect container to find its image name/tag
	cli, err := docker.GetClient()
	if err != nil {
		return "", fmt.Errorf("docker client error: %w", err)
	}

	inspect, err := cli.ContainerInspect(ctx, req.ContainerName)
	if err != nil || inspect.Config == nil {
		return "", fmt.Errorf("failed to inspect container %s: %w", req.ContainerName, err)
	}

	imageName := inspect.Config.Image
	if imageName == "" {
		imageName = inspect.Image
	}
	if imageName == "" {
		return "", fmt.Errorf("could not determine image for container %s", req.ContainerName)
	}

	// 2. Create temp file for the compressed docker image save
	tmpFile, err := os.CreateTemp("", "autohost-imgbackup-*.image.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpFilePath := tmpFile.Name()
	defer os.Remove(tmpFilePath)
	defer tmpFile.Close()

	// 3. Pipe `docker save <image>` through gzip writer into tmpFile
	gw := gzip.NewWriter(tmpFile)
	cmd := exec.CommandContext(ctx, "docker", "save", imageName)
	cmd.Stdout = gw
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	_ = gw.Close()
	_ = tmpFile.Close()

	if runErr != nil {
		stderr := strings.TrimSpace(errBuf.String())
		if stderr != "" {
			return "", fmt.Errorf("docker save failed: %s (%w)", stderr, runErr)
		}
		return "", fmt.Errorf("docker save failed: %w", runErr)
	}

	nowStr := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s.image.tar.gz", nowStr, req.ContainerName)

	return saveAndStoreBackup(ctx, StorageOptions{
		ContainerName:      req.ContainerName,
		BackupRecordID:     req.BackupRecordID,
		StorageDestination: req.StorageDestination,
		APIURL:             req.APIURL,
		NodeToken:          req.NodeToken,
		RetentionCount:     req.RetentionCount,
		Filename:           filename,
		TempFilePath:       tmpFilePath,
		S3:                 req.S3,
	}, startTime)
}

