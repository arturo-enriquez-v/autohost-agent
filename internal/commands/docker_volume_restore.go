// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"autohost-agent/internal/infra/docker"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/docker/docker/api/types/container"
)

// DockerVolumeRestore implements CommandWithOutput to download a backup from S3 / R2 and restore it to Docker volumes.
type DockerVolumeRestore struct{}

type RestorePayload struct {
	ContainerName string   `json:"container_name"`
	Volumes       []string `json:"volumes"`
	S3            S3Config `json:"s3"`
}

type RestoreResult struct {
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

func (c *DockerVolumeRestore) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *DockerVolumeRestore) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	startTime := time.Now()

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	var req RestorePayload
	if err := json.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to parse restore payload: %w", err)
	}

	if req.ContainerName == "" {
		return "", fmt.Errorf("container_name is required")
	}
	if !safeContainerName.MatchString(req.ContainerName) {
		return "", fmt.Errorf("invalid container name: %q", req.ContainerName)
	}
	if req.S3.Bucket == "" || req.S3.Endpoint == "" || req.S3.S3Key == "" {
		return "", fmt.Errorf("invalid S3/R2 storage configuration or s3_key missing")
	}

	vols := req.Volumes
	if len(vols) == 0 {
		detected, err := inspectContainerVolumes(ctx, req.ContainerName)
		if err != nil {
			return "", fmt.Errorf("failed to inspect container volumes: %w", err)
		}
		vols = detected
	}
	if len(vols) == 0 {
		return "", fmt.Errorf("no target volumes specified or detected for %q", req.ContainerName)
	}

	tmpDir, err := os.MkdirTemp("", "autohost-restore-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tarFile := filepath.Join(tmpDir, "backup.tar.gz")

	if err := downloadFromS3(ctx, req.S3, tarFile); err != nil {
		return "", fmt.Errorf("S3 download failed: %w", err)
	}

	cli, cliErr := docker.GetClient()
	if cliErr == nil {
		stopTimeout := 15
		_ = cli.ContainerStop(ctx, req.ContainerName, container.StopOptions{Timeout: &stopTimeout})
		defer func() {
			_ = cli.ContainerStart(context.Background(), req.ContainerName, container.StartOptions{})
		}()
	}

	dockerArgs := []string{"run", "-i", "--rm"}
	var cleanTargets []string
	for i, v := range vols {
		mountTarget := fmt.Sprintf("/target/vol_%d", i)
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:%s", v, mountTarget))
		cleanTargets = append(cleanTargets, fmt.Sprintf("%s/* %s/.[!.]*", mountTarget, mountTarget))
	}

	shScript := fmt.Sprintf("rm -rf %s 2>/dev/null || true; tar -xzf - --numeric-owner -C /target", strings.Join(cleanTargets, " "))
	dockerArgs = append(dockerArgs, "alpine", "sh", "-c", shScript)

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	tarFileReader, err := os.Open(tarFile)
	if err != nil {
		return "", fmt.Errorf("failed to open downloaded archive: %w", err)
	}
	defer tarFileReader.Close()
	cmd.Stdin = tarFileReader

	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker tar extraction failed: %s (%w)", errBuf.String(), err)
	}

	duration := time.Since(startTime).Milliseconds()
	result := RestoreResult{
		Status:     "completed",
		DurationMS: duration,
	}
	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

func downloadFromS3(ctx context.Context, cfg S3Config, destFile string) error {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
	}
	httpClient := &http.Client{Transport: tr}

	s3Opts := s3.Options{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		HTTPClient:   httpClient,
	}

	client := s3.New(s3Opts)

	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(cfg.S3Key),
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(destFile)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
