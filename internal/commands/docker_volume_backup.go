// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"autohost-agent/internal/infra/docker"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// DockerVolumeBackup implements CommandWithOutput to back up Docker container volumes to S3 / R2.
type DockerVolumeBackup struct{}

type S3Config struct {
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	S3Key           string `json:"s3_key"`
}

type BackupPayload struct {
	ContainerName  string   `json:"container_name"`
	Volumes        []string `json:"volumes"`
	PauseContainer bool     `json:"pause_container"`
	S3             S3Config `json:"s3"`
}

type BackupResult struct {
	S3Key         string `json:"s3_key"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	DurationMS    int64  `json:"duration_ms"`
}

func (c *DockerVolumeBackup) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *DockerVolumeBackup) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	startTime := time.Now()

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	var req BackupPayload
	if err := json.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to parse backup payload: %w", err)
	}

	if req.ContainerName == "" {
		return "", fmt.Errorf("container_name is required")
	}
	if !safeContainerName.MatchString(req.ContainerName) {
		return "", fmt.Errorf("invalid container name: %q", req.ContainerName)
	}
	if req.S3.Bucket == "" || req.S3.Endpoint == "" || req.S3.AccessKeyID == "" {
		return "", fmt.Errorf("invalid S3/R2 storage configuration")
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
		return "", fmt.Errorf("no volumes found to backup for container %q", req.ContainerName)
	}

	tmpFile, err := os.CreateTemp("", "autohost-backup-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tarFilePath := tmpFile.Name()
	defer os.Remove(tarFilePath)
	defer tmpFile.Close()

	if req.PauseContainer {
		cli, cliErr := docker.GetClient()
		if cliErr == nil {
			if err := cli.ContainerPause(ctx, req.ContainerName); err != nil {
				return "", fmt.Errorf("docker pause %s failed: %w", req.ContainerName, err)
			}
			defer func() {
				_ = cli.ContainerUnpause(context.Background(), req.ContainerName)
			}()
		}
	}

	dockerArgs := []string{"run", "--rm"}
	for i, v := range vols {
		mountTarget := fmt.Sprintf("/source/vol_%d", i)
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:%s:ro", v, mountTarget))
	}
	dockerArgs = append(dockerArgs, "alpine", "tar", "-czf", "-", "--numeric-owner", "-C", "/source", ".")

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	cmd.Stdout = tmpFile
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker tar archive failed: %s (%w)", errBuf.String(), err)
	}
	_ = tmpFile.Close()

	fi, err := os.Stat(tarFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat backup file: %w", err)
	}
	fileSizeBytes := fi.Size()

	s3Key := req.S3.S3Key
	if s3Key == "" {
		s3Key = fmt.Sprintf("backups/%s/%s_%s.tar.gz", req.ContainerName, req.ContainerName, time.Now().Format("20060102_150405"))
	}

	if err := uploadToS3(ctx, req.S3, tarFilePath, s3Key); err != nil {
		return "", fmt.Errorf("S3 upload failed: %w", err)
	}

	duration := time.Since(startTime).Milliseconds()

	result := BackupResult{
		S3Key:         s3Key,
		FileSizeBytes: fileSizeBytes,
		DurationMS:    duration,
	}

	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

func inspectContainerVolumes(ctx context.Context, containerName string) ([]string, error) {
	cli, err := docker.GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	inspect, err := cli.ContainerInspect(ctx, containerName)
	if err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", containerName, err)
	}

	var volumes []string
	for _, m := range inspect.Mounts {
		if m.Name != "" {
			volumes = append(volumes, m.Name)
		} else if m.Source != "" {
			volumes = append(volumes, m.Source)
		}
	}
	return volumes, nil
}

func uploadToS3(ctx context.Context, cfg S3Config, filePath, key string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

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

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	return err
}
