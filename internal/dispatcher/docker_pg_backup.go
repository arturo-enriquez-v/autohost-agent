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

// DockerPgBackup implements CommandWithOutput to create a PostgreSQL logical backup
// using pg_dumpall (or pg_dump for a specific database) executed inside the container,
// then uploads the compressed SQL dump to S3 / R2.
type DockerPgBackup struct{}

type PgBackupPayload struct {
	ContainerName string   `json:"container_name"`
	PgUser        string   `json:"pg_user"`
	Database      string   `json:"database"` // empty = pg_dumpall
	S3            S3Config `json:"s3"`
}

type PgBackupResult struct {
	S3Key         string `json:"s3_key"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	DurationMS    int64  `json:"duration_ms"`
}

func (c *DockerPgBackup) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *DockerPgBackup) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	startTime := time.Now()

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	var req PgBackupPayload
	if err := json.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to parse pg backup payload: %w", err)
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

	pgEnv := getPgContainerEnv(ctx, req.ContainerName)

	pgUser := req.PgUser
	if pgUser == "" {
		if pgEnv.User != "" {
			pgUser = pgEnv.User
		} else {
			pgUser = "postgres"
		}
	}

	// Create temp file for the compressed SQL dump
	tmpFile, err := os.CreateTemp("", "autohost-pgbackup-*.sql.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	dumpFilePath := tmpFile.Name()
	defer os.Remove(dumpFilePath)
	defer tmpFile.Close()

	// Build the docker exec command args:
	// For a specific database: pg_dump --clean --if-exists -U <user> <database>
	// For all databases:       pg_dumpall --clean --if-exists -U <user>
	var dumpCmd string
	if req.Database != "" {
		dumpCmd = fmt.Sprintf("pg_dump --clean --if-exists -U %s %s", pgUser, req.Database)
	} else {
		dumpCmd = fmt.Sprintf("pg_dumpall --clean --if-exists -U %s", pgUser)
	}

	var envArgs []string
	if pgEnv.Password != "" {
		envArgs = append(envArgs, "-e", fmt.Sprintf("PGPASSWORD=%s", pgEnv.Password))
	}

	dockerArgs := []string{"exec"}
	dockerArgs = append(dockerArgs, envArgs...)
	dockerArgs = append(dockerArgs, req.ContainerName, "sh", "-c", dumpCmd)

	gw := gzip.NewWriter(tmpFile)
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	cmd.Stdout = gw
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	_ = gw.Close()
	_ = tmpFile.Close()

	stderrMsg := strings.TrimSpace(errBuf.String())
	if runErr != nil {
		if stderrMsg != "" {
			return "", fmt.Errorf("pg_dump failed: %s (%w)", stderrMsg, runErr)
		}
		return "", fmt.Errorf("pg_dump failed: %w", runErr)
	}

	fi, err := os.Stat(dumpFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat dump file: %w", err)
	}
	fileSizeBytes := fi.Size()

	// Sanity check: pg_dump with errors can still produce a tiny output
	if fileSizeBytes < 50 {
		if stderrMsg != "" {
			return "", fmt.Errorf("pg_dump produced a suspiciously small output (%d bytes): %s", fileSizeBytes, stderrMsg)
		}
		return "", fmt.Errorf("pg_dump produced a suspiciously small output (%d bytes), possible error in dump", fileSizeBytes)
	}

	s3Key := req.S3.S3Key
	if s3Key == "" {
		s3Key = fmt.Sprintf("backups/%s/%s_%s.sql.gz", req.ContainerName, req.ContainerName, time.Now().Format("20060102_150405"))
	}

	if err := uploadToS3(ctx, req.S3, dumpFilePath, s3Key); err != nil {
		return "", fmt.Errorf("S3 upload failed: %w", err)
	}

	duration := time.Since(startTime).Milliseconds()

	result := PgBackupResult{
		S3Key:         s3Key,
		FileSizeBytes: fileSizeBytes,
		DurationMS:    duration,
	}

	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

type PgContainerEnv struct {
	User     string
	Password string
	Database string
}

// getPgContainerEnv inspects container env vars to extract POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB if present.
func getPgContainerEnv(ctx context.Context, containerName string) PgContainerEnv {
	cli, err := docker.GetClient()
	if err != nil {
		return PgContainerEnv{}
	}

	inspect, err := cli.ContainerInspect(ctx, containerName)
	if err != nil || inspect.Config == nil {
		return PgContainerEnv{}
	}

	var res PgContainerEnv
	for _, env := range inspect.Config.Env {
		if strings.HasPrefix(env, "POSTGRES_PASSWORD=") {
			res.Password = strings.TrimPrefix(env, "POSTGRES_PASSWORD=")
		} else if strings.HasPrefix(env, "PGPASSWORD=") {
			res.Password = strings.TrimPrefix(env, "PGPASSWORD=")
		} else if strings.HasPrefix(env, "POSTGRES_USER=") {
			res.User = strings.TrimPrefix(env, "POSTGRES_USER=")
		} else if strings.HasPrefix(env, "POSTGRES_DB=") {
			res.Database = strings.TrimPrefix(env, "POSTGRES_DB=")
		}
	}
	return res
}
