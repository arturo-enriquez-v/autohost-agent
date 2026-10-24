// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"

	pb "autohost-agent/internal/grpc/nodepb"
	"autohost-agent/internal/infra/docker"
	"autohost-agent/internal/security"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

const (
	// MaxSourcesPerBundle limits how many sources can be collected in one request.
	MaxSourcesPerBundle = 5

	// DefaultLinesPerSource is used when the request doesn't specify a line count.
	DefaultLinesPerSource int32 = 200

	// MaxLinesPerSource caps the lines per source to avoid excessive memory use.
	MaxLinesPerSourceCap int32 = 500

	// collectTimeout is the max duration for a single source collection.
	collectTimeout = 30 * time.Second
)

// clampCollectLines restricts the requested lines for one-shot collection.
func clampCollectLines(lines int32) int32 {
	if lines <= 0 {
		return DefaultLinesPerSource
	}
	if lines > MaxLinesPerSourceCap {
		return MaxLinesPerSourceCap
	}
	return lines
}

// collectJournalctlLogs runs journalctl in one-shot mode (no -f) and returns
// sanitized log output.
func collectJournalctlLogs(ctx context.Context, unit string, lines int32) (*pb.LogSourceBundle, error) {
	ctx, cancel := context.WithTimeout(ctx, collectTimeout)
	defer cancel()

	args := []string{"--output=short-iso", "--no-pager", "-n", fmt.Sprintf("%d", lines)}
	if unit != "" {
		args = append(args, "-u", unit)
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("journalctl %s: %w (stderr: %s)", unit, err, stderr.String())
	}

	rawLines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")

	// Sanitize and truncate each line.
	var sanitized []string
	for _, line := range rawLines {
		if line == "" {
			continue
		}
		line = sanitizeLogLine(line)          // existing transport-level truncation
		line = security.SanitizeLogLine(line) // secret redaction
		sanitized = append(sanitized, line)
	}

	sourceName := "journalctl"
	if unit != "" {
		sourceName = "journalctl:" + unit
	}

	return &pb.LogSourceBundle{
		Source:    sourceName,
		RawLogs:  strings.Join(sanitized, "\n"),
		LineCount: int32(len(sanitized)),
	}, nil
}

// collectDockerLogs retrieves logs via Docker Engine API in one-shot mode and returns
// sanitized log output, demultiplexing streams safely with stdcopy.StdCopy.
func collectDockerLogs(ctx context.Context, containerName string, lines int32) (*pb.LogSourceBundle, error) {
	ctx, cancel := context.WithTimeout(ctx, collectTimeout)
	defer cancel()

	cli, err := docker.GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	inspect, err := cli.ContainerInspect(ctx, containerName)
	if err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", containerName, err)
	}

	logsReader, err := cli.ContainerLogs(ctx, containerName, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       fmt.Sprintf("%d", lines),
	})
	if err != nil {
		return nil, fmt.Errorf("docker logs %s: %w", containerName, err)
	}
	defer logsReader.Close()

	var combined string
	if inspect.Config != nil && inspect.Config.Tty {
		raw, readErr := io.ReadAll(logsReader)
		if readErr != nil {
			return nil, fmt.Errorf("docker read logs %s: %w", containerName, readErr)
		}
		combined = string(raw)
	} else {
		var stdout, stderr bytes.Buffer
		if _, copyErr := stdcopy.StdCopy(&stdout, &stderr, logsReader); copyErr != nil && copyErr != io.EOF {
			return nil, fmt.Errorf("docker demux logs %s: %w", containerName, copyErr)
		}
		combined = stdout.String() + stderr.String()
	}

	rawLines := strings.Split(strings.TrimRight(combined, "\n"), "\n")

	var sanitized []string
	for _, line := range rawLines {
		if line == "" {
			continue
		}
		line = sanitizeLogLine(line)
		line = security.SanitizeLogLine(line)
		sanitized = append(sanitized, line)
	}

	return &pb.LogSourceBundle{
		Source:    "docker:" + containerName,
		RawLogs:  strings.Join(sanitized, "\n"),
		LineCount: int32(len(sanitized)),
	}, nil
}

// listRunningContainers returns the names of all running Docker containers.
func listRunningContainers(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cli, err := docker.GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	containers, err := cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker list containers: %w", err)
	}

	var names []string
	for _, c := range containers {
		if len(c.Names) > 0 {
			name := strings.TrimPrefix(c.Names[0], "/")
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// collectAllSources collects logs from the requested sources (and optionally
// all running containers) and returns the bundled results.
func collectAllSources(ctx context.Context, req *pb.CollectLogsPayload) ([]*pb.LogSourceBundle, error) {
	lines := clampCollectLines(req.GetLinesPerSource())
	var bundles []*pb.LogSourceBundle

	// Collect explicit sources.
	for i, source := range req.GetSources() {
		if i >= MaxSourcesPerBundle {
			log.Printf("⚠️  log collector: truncating to %d sources", MaxSourcesPerBundle)
			break
		}

		var bundle *pb.LogSourceBundle
		var err error

		if strings.HasPrefix(source, "docker:") {
			containerName := strings.TrimPrefix(source, "docker:")
			bundle, err = collectDockerLogs(ctx, containerName, lines)
		} else {
			bundle, err = collectJournalctlLogs(ctx, source, lines)
		}

		if err != nil {
			log.Printf("⚠️  log collector: %v", err)
			// Include a bundle with the error so the diagnostic report is complete.
			bundle = &pb.LogSourceBundle{
				Source:    source,
				RawLogs:  "ERROR: " + err.Error(),
				LineCount: 0,
			}
		}
		bundles = append(bundles, bundle)
	}

	// If include_containers is true, also collect from all running containers
	// (excluding any already explicitly listed).
	if req.GetIncludeContainers() {
		containers, err := listRunningContainers(ctx)
		if err != nil {
			log.Printf("⚠️  log collector: list containers: %v", err)
		} else {
			existing := make(map[string]bool)
			for _, s := range req.GetSources() {
				existing[s] = true
				if strings.HasPrefix(s, "docker:") {
					existing[strings.TrimPrefix(s, "docker:")] = true
				}
			}

			for _, name := range containers {
				if existing["docker:"+name] || existing[name] {
					continue
				}
				if len(bundles) >= MaxSourcesPerBundle {
					break
				}
				bundle, err := collectDockerLogs(ctx, name, lines)
				if err != nil {
					log.Printf("⚠️  log collector: docker %s: %v", name, err)
					continue
				}
				bundles = append(bundles, bundle)
			}
		}
	}

	return bundles, nil
}
