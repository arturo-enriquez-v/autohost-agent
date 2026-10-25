// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"autohost-agent/internal/platform/paths"
)

const maxRecordedJobs = 1000

// JobRecord records the execution metadata of a processed job for idempotency.
type JobRecord struct {
	JobID       string    `json:"job_id"`
	Status      string    `json:"status"`
	ProcessedAt time.Time `json:"processed_at"`
}

// Store manages offline-resilient local persistence for desired state and job idempotency.
type Store interface {
	RecordJob(jobID string, status string) error
	HasJobBeenProcessed(jobID string) bool
	SaveDesiredState(key string, state any) error
	GetDesiredState(key string, target any) error
}

type FileStore struct {
	mu       sync.RWMutex
	dir      string
	jobsFile string
	jobs     map[string]JobRecord
}

// New creates a new FileStore using the default /var/lib/autohost/state directory.
func New() (*FileStore, error) {
	return NewWithDir(paths.GetSubdir("state"))
}

// NewWithDir creates a FileStore backed by a custom directory (useful for testing).
func NewWithDir(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("state store: failed to create dir %s: %w", dir, err)
	}

	fs := &FileStore{
		dir:      dir,
		jobsFile: filepath.Join(dir, "jobs_idempotency.json"),
		jobs:     make(map[string]JobRecord),
	}

	if err := fs.loadJobs(); err != nil {
		// Log warning but don't fail startup if file was missing/empty
		fs.jobs = make(map[string]JobRecord)
	}

	return fs, nil
}

func (s *FileStore) loadJobs() error {
	data, err := os.ReadFile(s.jobsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var records []JobRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}

	for _, r := range records {
		s.jobs[r.JobID] = r
	}
	return nil
}

// HasJobBeenProcessed checks whether a given job has already been executed.
func (s *FileStore) HasJobBeenProcessed(jobID string) bool {
	if jobID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.jobs[jobID]
	return exists
}

// RecordJob records a processed job ID to guarantee idempotency.
func (s *FileStore) RecordJob(jobID string, status string) error {
	if jobID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.jobs[jobID] = JobRecord{
		JobID:       jobID,
		Status:      status,
		ProcessedAt: time.Now().UTC(),
	}

	// Evict oldest if exceeding capacity
	if len(s.jobs) > maxRecordedJobs {
		var oldestID string
		var oldestTime time.Time
		first := true
		for id, r := range s.jobs {
			if first || r.ProcessedAt.Before(oldestTime) {
				oldestID = id
				oldestTime = r.ProcessedAt
				first = false
			}
		}
		delete(s.jobs, oldestID)
	}

	records := make([]JobRecord, 0, len(s.jobs))
	for _, r := range s.jobs {
		records = append(records, r)
	}

	return s.atomicWriteJSON(s.jobsFile, records)
}

// SaveDesiredState persists arbitrary state JSON atomically by key (e.g., "apps", "network").
func (s *FileStore) SaveDesiredState(key string, state any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := filepath.Join(s.dir, fmt.Sprintf("desired_%s.json", key))
	return s.atomicWriteJSON(filePath, state)
}

// GetDesiredState reads and unmarshals the desired state for a key into target.
func (s *FileStore) GetDesiredState(key string, target any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := filepath.Join(s.dir, fmt.Sprintf("desired_%s.json", key))
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, target)
}

// atomicWriteJSON writes data to a temporary file in the same directory and renames it atomically.
func (s *FileStore) atomicWriteJSON(finalPath string, data any) error {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("state store: marshal failed: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", finalPath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, bytes, 0600); err != nil {
		return fmt.Errorf("state store: write temp failed: %w", err)
	}

	if err := os.Rename(tmpFile, finalPath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("state store: atomic rename failed: %w", err)
	}

	return nil
}
