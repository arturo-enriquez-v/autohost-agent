// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
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
	pb "autohost-agent/internal/transport/nodepb"
)

func (c *GRPCClient) handleFileOpRequest(ctx context.Context, req *pb.FileOperationRequestPayload, out chan<- *pb.NodeMessage) {
	if req == nil || req.GetRequestId() == "" {
		return
	}

	resp := executeLocalFileOp(ctx, req)

	select {
	case out <- &pb.NodeMessage{
		Payload: &pb.NodeMessage_FileOpResponse{
			FileOpResponse: resp,
		},
	}:
	case <-ctx.Done():
	}
}

func executeLocalFileOp(ctx context.Context, req *pb.FileOperationRequestPayload) *pb.FileOperationResponsePayload {
	reqID := req.GetRequestId()
	sharedRoot := filepath.Clean(paths.GetSharedDir())
	if err := os.MkdirAll(sharedRoot, 0755); err != nil {
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   false,
			Error:     fmt.Sprintf("failed to initialize shared directory: %v", err),
		}
	}

	fullPath, err := paths.SafeSharedPath(req.GetPath())
	if err != nil {
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   false,
			Error:     err.Error(),
		}
	}

	switch req.GetOpType() {
	case pb.FileOpType_FILE_OP_LIST:
		entries, listErr := listSharedDirEntries(sharedRoot, fullPath)
		if listErr != nil {
			return &pb.FileOperationResponsePayload{
				RequestId: reqID,
				Success:   false,
				Error:     listErr.Error(),
			}
		}
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   true,
			Entries:   entries,
		}

	case pb.FileOpType_FILE_OP_MKDIR:
		if fullPath == sharedRoot {
			return &pb.FileOperationResponsePayload{
				RequestId: reqID,
				Success:   false,
				Error:     "directory path is required",
			}
		}
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return &pb.FileOperationResponsePayload{
				RequestId: reqID,
				Success:   false,
				Error:     fmt.Sprintf("mkdir failed: %v", err),
			}
		}
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   true,
		}

	case pb.FileOpType_FILE_OP_DELETE:
		if fullPath == sharedRoot {
			return &pb.FileOperationResponsePayload{
				RequestId: reqID,
				Success:   false,
				Error:     "cannot delete root shared directory",
			}
		}
		if err := os.RemoveAll(fullPath); err != nil {
			return &pb.FileOperationResponsePayload{
				RequestId: reqID,
				Success:   false,
				Error:     fmt.Sprintf("delete failed: %v", err),
			}
		}
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   true,
		}

	case pb.FileOpType_FILE_OP_PUSH_TO_URL:
		if err := pushSharedFileToURL(ctx, fullPath, req.GetTransferUrl(), req.GetAuthToken()); err != nil {
			log.Printf("⚠️  file_op push failed (%s): %v", req.GetPath(), err)
			return &pb.FileOperationResponsePayload{
				RequestId: reqID,
				Success:   false,
				Error:     err.Error(),
			}
		}
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   true,
		}

	case pb.FileOpType_FILE_OP_PULL_FROM_URL:
		if err := pullSharedFileFromURL(ctx, sharedRoot, fullPath, req.GetTransferUrl(), req.GetAuthToken()); err != nil {
			log.Printf("⚠️  file_op pull failed (%s): %v", req.GetPath(), err)
			return &pb.FileOperationResponsePayload{
				RequestId: reqID,
				Success:   false,
				Error:     err.Error(),
			}
		}
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   true,
		}

	default:
		return &pb.FileOperationResponsePayload{
			RequestId: reqID,
			Success:   false,
			Error:     fmt.Sprintf("unsupported file operation: %v", req.GetOpType()),
		}
	}
}

func listSharedDirEntries(sharedRoot, dirPath string) ([]*pb.FileEntryInfo, error) {
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	result := make([]*pb.FileEntryInfo, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".autohost-tmp-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entryFullPath := filepath.Join(dirPath, entry.Name())
		rel, err := filepath.Rel(sharedRoot, entryFullPath)
		if err != nil {
			rel = entry.Name()
		}
		relSlash := "/" + filepath.ToSlash(rel)

		result = append(result, &pb.FileEntryInfo{
			Name:       entry.Name(),
			Path:       relSlash,
			IsDir:      entry.IsDir(),
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime().Unix(),
			Mode:       info.Mode().String(),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

func pushSharedFileToURL(ctx context.Context, filePath, transferURL, transferToken string) error {
	if strings.TrimSpace(transferURL) == "" {
		return fmt.Errorf("transfer URL is empty")
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("cannot transfer a directory directly; select a file")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, transferURL, f)
	if err != nil {
		return fmt.Errorf("failed to create transfer request: %w", err)
	}
	httpReq.ContentLength = info.Size()
	httpReq.Header.Set("Content-Type", "application/octet-stream")
	if transferToken != "" {
		httpReq.Header.Set("X-Transfer-Token", transferToken)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("transfer upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("transfer upload returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}

func pullSharedFileFromURL(ctx context.Context, sharedRoot, destPath, transferURL, transferToken string) error {
	if strings.TrimSpace(transferURL) == "" {
		return fmt.Errorf("transfer URL is empty")
	}
	if destPath == sharedRoot {
		return fmt.Errorf("destination file path is required")
	}

	parentDir := filepath.Dir(destPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, transferURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create transfer request: %w", err)
	}
	if transferToken != "" {
		httpReq.Header.Set("X-Transfer-Token", transferToken)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("transfer download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("transfer download returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	tmpFile, err := os.CreateTemp(parentDir, ".autohost-tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write transferred file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		_ = err
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("failed to finalize transferred file: %w", err)
	}
	return nil
}
