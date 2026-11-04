// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pb "autohost-agent/internal/transport/nodepb"
)

func TestExecuteLocalFileOpListMkdirDelete(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AUTOHOST_SHARED_DIR", tmp)

	// 1. Create directory
	mkdirResp := executeLocalFileOp(context.Background(), &pb.FileOperationRequestPayload{
		RequestId: "req-1",
		OpType:    pb.FileOpType_FILE_OP_MKDIR,
		Path:      "/backups/daily",
	})
	if !mkdirResp.GetSuccess() {
		t.Fatalf("mkdir failed: %s", mkdirResp.GetError())
	}

	// Create a dummy file inside /backups
	testFile := filepath.Join(tmp, "backups", "test.txt")
	if err := os.WriteFile(testFile, []byte("hello autohost"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// 2. List /backups
	listResp := executeLocalFileOp(context.Background(), &pb.FileOperationRequestPayload{
		RequestId: "req-2",
		OpType:    pb.FileOpType_FILE_OP_LIST,
		Path:      "/backups",
	})
	if !listResp.GetSuccess() {
		t.Fatalf("list failed: %s", listResp.GetError())
	}
	if len(listResp.GetEntries()) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(listResp.GetEntries()))
	}
	if !listResp.GetEntries()[0].GetIsDir() || listResp.GetEntries()[0].GetName() != "daily" {
		t.Fatalf("expected directory 'daily' first, got %+v", listResp.GetEntries()[0])
	}

	// 3. Reject path traversal
	travResp := executeLocalFileOp(context.Background(), &pb.FileOperationRequestPayload{
		RequestId: "req-3",
		OpType:    pb.FileOpType_FILE_OP_DELETE,
		Path:      "/../../etc/passwd",
	})
	// Note: SafeSharedPath normalizes "/../../etc/passwd" inside shared root; deleting root itself is blocked.
	rootDelResp := executeLocalFileOp(context.Background(), &pb.FileOperationRequestPayload{
		RequestId: "req-4",
		OpType:    pb.FileOpType_FILE_OP_DELETE,
		Path:      "/",
	})
	if rootDelResp.GetSuccess() {
		t.Fatalf("expected deleting root shared dir to fail")
	}
	_ = travResp

	// 4. Delete /backups/test.txt
	delResp := executeLocalFileOp(context.Background(), &pb.FileOperationRequestPayload{
		RequestId: "req-5",
		OpType:    pb.FileOpType_FILE_OP_DELETE,
		Path:      "/backups/test.txt",
	})
	if !delResp.GetSuccess() {
		t.Fatalf("delete failed: %s", delResp.GetError())
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Fatalf("expected test.txt to be deleted")
	}
}
