// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// DockerPgRestore implements CommandWithOutput to restore a PostgreSQL logical backup
// by downloading the compressed SQL dump from S3 / R2 and piping it into psql.
type DockerPgRestore struct{}

type PgRestorePayload struct {
	ContainerName string   `json:"container_name"`
	PgUser        string   `json:"pg_user"`
	Database      string   `json:"database"` // empty = restore to default (used with pg_dumpall output)
	S3            S3Config `json:"s3"`
}

type PgRestoreResult struct {
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

func (c *DockerPgRestore) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *DockerPgRestore) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	startTime := time.Now()

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	var req PgRestorePayload
	if err := json.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to parse pg restore payload: %w", err)
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

	pgEnv := getPgContainerEnv(ctx, req.ContainerName)

	pgUser := req.PgUser
	if pgUser == "" {
		if pgEnv.User != "" {
			pgUser = pgEnv.User
		} else {
			pgUser = "postgres"
		}
	}

	// Download the SQL dump from S3
	tmpFile, err := os.CreateTemp("", "autohost-pgrestore-*.sql.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	dumpFilePath := tmpFile.Name()
	defer os.Remove(dumpFilePath)
	_ = tmpFile.Close()

	if err := downloadFromS3(ctx, req.S3, dumpFilePath); err != nil {
		return "", fmt.Errorf("S3 download failed: %w", err)
	}

	// Pre-clean existing target databases so restore cleanly replaces existing data
	preCleanDatabases(ctx, dumpFilePath, req.Database, req.ContainerName, pgUser, pgEnv.Password)

	// Read compressed file and pipe gunzipped stream to docker exec -i ... psql
	f, err := os.Open(dumpFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to open dump file: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	// Target database resolution:
	// If explicit single database set in payload, use it.
	// For cluster dumps (pg_dumpall), initial connection MUST be "postgres" maintenance db
	// so psql can create/connect to user databases defined in the dump.
	var psqlTarget string
	if req.Database != "" {
		psqlTarget = req.Database
	} else {
		psqlTarget = "postgres"
	}

	var envArgs []string
	if pgEnv.Password != "" {
		envArgs = append(envArgs, "-e", fmt.Sprintf("PGPASSWORD=%s", pgEnv.Password))
	}

	dockerArgs := []string{"exec", "-i"}
	dockerArgs = append(dockerArgs, envArgs...)
	dockerArgs = append(dockerArgs, req.ContainerName, "psql", "-U", pgUser, "-d", psqlTarget)

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	cmd.Stdin = gr
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	cmd.Stdout = nil

	if err := cmd.Run(); err != nil {
		stderrMsg := strings.TrimSpace(errBuf.String())
		if stderrMsg != "" {
			return "", fmt.Errorf("psql restore failed: %s (%w)", stderrMsg, err)
		}
		return "", fmt.Errorf("psql restore failed: %w", err)
	}

	duration := time.Since(startTime).Milliseconds()
	result := PgRestoreResult{
		Status:     "completed",
		DurationMS: duration,
	}
	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

var reCreateDB = regexp.MustCompile(`(?i)CREATE\s+DATABASE\s+(?:"([^"]+)"|'([^']+)'|([a-zA-Z0-9_-]+))`)

func preCleanDatabases(ctx context.Context, dumpFilePath string, reqDatabase string, containerName, pgUser, pgPassword string) {
	var envArgs []string
	if pgPassword != "" {
		envArgs = append(envArgs, "-e", fmt.Sprintf("PGPASSWORD=%s", pgPassword))
	}

	if reqDatabase != "" {
		// Single database restore: drop and recreate public schema
		cleanArgs := []string{"exec", "-i"}
		cleanArgs = append(cleanArgs, envArgs...)
		cleanArgs = append(cleanArgs, containerName, "psql", "-U", pgUser, "-d", reqDatabase, "-c",
			"DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO public;")
		_ = exec.CommandContext(ctx, "docker", cleanArgs...).Run()
		return
	}

	// Cluster dump (pg_dumpall): parse SQL for created databases and drop them first
	f, err := os.Open(dumpFilePath)
	if err != nil {
		return
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return
	}
	defer gr.Close()

	sqlBytes, err := io.ReadAll(gr)
	if err != nil {
		return
	}

	sqlContent := string(sqlBytes)
	matches := reCreateDB.FindAllStringSubmatch(sqlContent, -1)
	seen := make(map[string]bool)
	var dbsToDrop []string
	for _, m := range matches {
		db := m[1]
		if db == "" {
			db = m[2]
		}
		if db == "" {
			db = m[3]
		}
		db = strings.TrimSpace(db)
		if db != "" && db != "postgres" && db != "template1" && db != "template0" && !seen[db] {
			seen[db] = true
			dbsToDrop = append(dbsToDrop, db)
		}
	}

	for _, db := range dbsToDrop {
		// 1. Terminate any active sessions connecting to this database
		termArgs := []string{"exec", "-i"}
		termArgs = append(termArgs, envArgs...)
		termArgs = append(termArgs, containerName, "psql", "-U", pgUser, "-d", "postgres", "-c",
			fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();", db))
		_ = exec.CommandContext(ctx, "docker", termArgs...).Run()

		// 2. Drop the database as a standalone statement (DROP DATABASE cannot run in a multi-statement transaction)
		dropArgs := []string{"exec", "-i"}
		dropArgs = append(dropArgs, envArgs...)
		dropArgs = append(dropArgs, containerName, "psql", "-U", pgUser, "-d", "postgres", "-c",
			fmt.Sprintf("DROP DATABASE IF EXISTS \"%s\" WITH (FORCE);", db))

		var dropErr bytes.Buffer
		cmd := exec.CommandContext(ctx, "docker", dropArgs...)
		cmd.Stderr = &dropErr
		if err := cmd.Run(); err != nil {
			// Fallback without WITH (FORCE) for older PostgreSQL engines
			fallbackArgs := []string{"exec", "-i"}
			fallbackArgs = append(fallbackArgs, envArgs...)
			fallbackArgs = append(fallbackArgs, containerName, "psql", "-U", pgUser, "-d", "postgres", "-c",
				fmt.Sprintf("DROP DATABASE IF EXISTS \"%s\";", db))
			_ = exec.CommandContext(ctx, "docker", fallbackArgs...).Run()
		}
	}
}
