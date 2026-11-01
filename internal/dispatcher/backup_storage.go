// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"autohost-agent/internal/platform/paths"
	"gopkg.in/yaml.v3"
)

type StorageOptions struct {
	ContainerName      string
	BackupRecordID     string
	StorageDestination string // "local", "local_and_parent", "s3"
	APIURL             string
	NodeID             string
	NodeToken          string
	RetentionCount     int
	Filename           string
	TempFilePath       string
	S3                 S3Config
}

type GenericBackupResult struct {
	LocalPath     string `json:"local_path,omitempty"`
	ParentPath    string `json:"parent_path,omitempty"`
	S3Key         string `json:"s3_key,omitempty"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	DurationMS    int64  `json:"duration_ms"`
}

func saveAndStoreBackup(ctx context.Context, opts StorageOptions, startTime time.Time) (string, error) {
	fi, err := os.Stat(opts.TempFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat backup file: %w", err)
	}
	fileSizeBytes := fi.Size()

	dest := strings.ToLower(strings.TrimSpace(opts.StorageDestination))
	if dest == "" {
		dest = "local_and_parent"
	}

	result := GenericBackupResult{
		FileSizeBytes: fileSizeBytes,
		DurationMS:    time.Since(startTime).Milliseconds(),
	}

	// 1. S3 / R2 upload mode
	if dest == "s3" {
		if opts.S3.Bucket == "" || opts.S3.Endpoint == "" || opts.S3.AccessKeyID == "" {
			return "", fmt.Errorf("invalid S3 storage configuration")
		}
		s3Key := opts.S3.S3Key
		if s3Key == "" {
			s3Key = fmt.Sprintf("backups/%s/%s", opts.ContainerName, opts.Filename)
		}
		if err := uploadToS3(ctx, opts.S3, opts.TempFilePath, s3Key); err != nil {
			return "", fmt.Errorf("S3 upload failed: %w", err)
		}
		result.S3Key = s3Key
		resBytes, _ := json.Marshal(result)
		return string(resBytes), nil
	}

	// 2. Local storage mode (on this node)
	baseDir := strings.TrimSpace(os.Getenv("BACKUP_STORAGE_DIR"))
	if baseDir == "" {
		baseDir = paths.GetSubdir("backups")
	}
	localDir := filepath.Join(baseDir, opts.ContainerName)
	if err := os.MkdirAll(localDir, 0755); err != nil {
		// Fallback to /tmp if running without root permissions
		baseDir = filepath.Join(os.TempDir(), "autohost-backups")
		localDir = filepath.Join(baseDir, opts.ContainerName)
		if err := os.MkdirAll(localDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create local backup dir: %w", err)
		}
	}

	localPath := filepath.Join(localDir, opts.Filename)

	// Move or copy from temp to localPath
	if err := copyOrMoveFile(opts.TempFilePath, localPath); err != nil {
		return "", fmt.Errorf("failed to save local backup to %s: %w", localPath, err)
	}
	result.LocalPath = localPath

	// Cleanup old local backups based on retention count
	if opts.RetentionCount > 0 {
		cleanupOldBackups(localDir, opts.RetentionCount)
	}

	// 3. Replicate to parent API node if requested
	if dest == "local_and_parent" || dest == "replicate_parent" {
		parentPath, err := replicateToParent(ctx, opts, localPath)
		if err != nil {
			log.Printf("⚠️  warn: replication to parent API failed: %v (local backup preserved at %s)", err, localPath)
		} else {
			result.ParentPath = parentPath
			log.Printf("✅ Backup successfully replicated to parent API: %s", parentPath)
		}
	}

	result.DurationMS = time.Since(startTime).Milliseconds()
	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

func replicateToParent(ctx context.Context, opts StorageOptions, filePath string) (string, error) {
	apiURL := opts.APIURL
	nodeID := opts.NodeID
	token := opts.NodeToken

	// If missing, read from /etc/autohost/config.yaml
	if apiURL == "" || nodeID == "" || token == "" {
		if cfg, err := readLocalAgentConfig(); err == nil {
			if apiURL == "" {
				apiURL = cfg.APIURL
			}
			if nodeID == "" {
				nodeID = cfg.NodeID
			}
			if token == "" {
				token = cfg.AgentToken
			}
		}
	}

	if apiURL == "" || nodeID == "" {
		return "", fmt.Errorf("cannot replicate: api_url or node_id missing")
	}

	apiURL = strings.TrimRight(apiURL, "/")
	uploadURL := fmt.Sprintf("%s/v1/backups/upload/%s/%s?filename=%s", apiURL, nodeID, opts.BackupRecordID, opts.Filename)

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open backup file for replication: %w", err)
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, f)
	if err != nil {
		return "", fmt.Errorf("create replication request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{
		Timeout: 30 * time.Minute, // Allow long streams for big archives
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("replication stream failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("replication HTTP error %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var res struct {
		ParentPath string `json:"parent_path"`
	}
	_ = json.Unmarshal(bodyBytes, &res)
	return res.ParentPath, nil
}

type localConfigPartial struct {
	APIURL     string `yaml:"api_url"`
	NodeID     string `yaml:"node_id"`
	AgentToken string `yaml:"agent_token"`
}

func readLocalAgentConfig() (*localConfigPartial, error) {
	data, err := os.ReadFile("/etc/autohost/config.yaml")
	if err != nil {
		return nil, err
	}
	var cfg localConfigPartial
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func copyOrMoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Fall back to copy if across different partitions
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func cleanupOldBackups(dir string, maxCount int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var files []fileInfo

	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		info, err := e.Info()
		if err == nil {
			files = append(files, fileInfo{
				path:    filepath.Join(dir, e.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	if len(files) <= maxCount {
		return
	}

	// Sort newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	for i := maxCount; i < len(files); i++ {
		_ = os.Remove(files[i].path)
	}
}

func fetchBackupFile(ctx context.Context, localPath, parentDownloadURL, nodeToken string, s3Cfg S3Config, destFile string) error {
	// 1. If local path exists on disk, copy it
	if localPath != "" {
		if _, err := os.Stat(localPath); err == nil {
			return copyOrMoveFile(localPath, destFile)
		}
	}

	// 2. If parent download URL is provided, stream from parent API
	if parentDownloadURL != "" {
		if nodeToken == "" {
			if cfg, err := readLocalAgentConfig(); err == nil {
				nodeToken = cfg.AgentToken
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parentDownloadURL, nil)
		if err != nil {
			return fmt.Errorf("create download request: %w", err)
		}
		if nodeToken != "" {
			req.Header.Set("Authorization", "Bearer "+nodeToken)
		}

		client := &http.Client{Timeout: 30 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("download from parent API failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("download from parent failed (HTTP %d): %s", resp.StatusCode, string(body))
		}

		out, err := os.Create(destFile)
		if err != nil {
			return fmt.Errorf("create dest file: %w", err)
		}
		defer out.Close()

		if _, err := io.Copy(out, resp.Body); err != nil {
			return fmt.Errorf("write downloaded backup: %w", err)
		}
		return nil
	}

	// 3. Fallback to S3 if configured
	if s3Cfg.Bucket != "" && s3Cfg.Endpoint != "" && s3Cfg.S3Key != "" {
		return downloadFromS3(ctx, s3Cfg, destFile)
	}

	return fmt.Errorf("no valid backup source available (local path not found, no parent download URL, no S3 config)")
}
