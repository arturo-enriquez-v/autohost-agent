// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJobIdempotency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "autohost-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewWithDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}

	jobID := "job_abc_123"
	if store.HasJobBeenProcessed(jobID) {
		t.Fatal("expected job to not be processed initially")
	}

	if err := store.RecordJob(jobID, "success"); err != nil {
		t.Fatalf("failed to record job: %v", err)
	}

	if !store.HasJobBeenProcessed(jobID) {
		t.Fatal("expected job to be marked as processed")
	}

	// Reload from disk into a fresh instance to ensure persistence
	store2, err := NewWithDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}

	if !store2.HasJobBeenProcessed(jobID) {
		t.Fatal("expected persisted job to be detected after store reload")
	}
}

func TestDesiredStatePersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "autohost-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewWithDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}

	type AppState struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}

	desired := []AppState{
		{Name: "nextcloud", Status: "running"},
		{Name: "postgres", Status: "running"},
	}

	if err := store.SaveDesiredState("apps", desired); err != nil {
		t.Fatalf("failed to save desired state: %v", err)
	}

	// Verify file exists on disk
	expectedFile := filepath.Join(tmpDir, "desired_apps.json")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("expected state file to exist: %v", err)
	}

	// Retrieve into a target slice
	var retrieved []AppState
	if err := store.GetDesiredState("apps", &retrieved); err != nil {
		t.Fatalf("failed to retrieve desired state: %v", err)
	}

	if len(retrieved) != 2 || retrieved[0].Name != "nextcloud" || retrieved[1].Status != "running" {
		t.Fatalf("retrieved state does not match saved state: %+v", retrieved)
	}
}
