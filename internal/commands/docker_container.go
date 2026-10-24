// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"autohost-agent/internal/infra/docker"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// safeContainerName only allows characters valid in Docker container names,
// preventing path traversal or shell metacharacter injection.
var safeContainerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-.]{0,127}$`)

// execContainerCommand handles container.* job types dispatched from the dashboard.
//
// Supported patterns:
//
//	container.list                  → container list with live stats (JSON array)
//	container.start.<name>          → container start
//	container.stop.<name>           → container stop
//	container.restart.<name>        → container restart
//	container.stats.<name>          → container stats (JSON)
//	container.logs.<name>           → container logs (last 100 lines)
func execContainerCommand(ctx context.Context, jobType string) ExecuteResult {
	// jobType examples: "container.list", "container.restart.my-app"
	parts := strings.SplitN(jobType, ".", 3)
	if len(parts) < 2 || parts[0] != "container" {
		return ExecuteResult{Err: fmt.Errorf("invalid container command: %q", jobType)}
	}

	action := parts[1]
	switch action {
	case "list":
		return containerList(ctx)

	case "start", "stop", "restart":
		if len(parts) < 3 || parts[2] == "" {
			return ExecuteResult{Err: fmt.Errorf("container %s: missing container name", action)}
		}
		name := parts[2]
		if !safeContainerName.MatchString(name) {
			return ExecuteResult{Err: fmt.Errorf("container %s: invalid container name %q", action, name)}
		}
		return containerAction(ctx, action, name)

	case "stats":
		if len(parts) < 3 || parts[2] == "" {
			return ExecuteResult{Err: fmt.Errorf("container stats: missing container name")}
		}
		name := parts[2]
		if !safeContainerName.MatchString(name) {
			return ExecuteResult{Err: fmt.Errorf("container stats: invalid container name %q", name)}
		}
		return containerStats(ctx, name)

	case "logs":
		if len(parts) < 3 || parts[2] == "" {
			return ExecuteResult{Err: fmt.Errorf("container logs: missing container name")}
		}
		name := parts[2]
		if !safeContainerName.MatchString(name) {
			return ExecuteResult{Err: fmt.Errorf("container logs: invalid container name %q", name)}
		}
		return containerLogs(ctx, name)

	default:
		return ExecuteResult{Err: fmt.Errorf("unknown container action %q in %q", action, jobType)}
	}
}

// containerInfo is the JSON shape returned for each container in container.list.
type containerInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	CPU     string `json:"cpu,omitempty"`
	Mem     string `json:"mem,omitempty"`
	MemPerc string `json:"memPerc,omitempty"`
}

// ContainerEntry is the public version of containerInfo used by the transport
// layer to construct proto messages without going through JSON marshaling.
type ContainerEntry struct {
	ID      string
	Name    string
	Image   string
	State   string
	Status  string
	CPU     string
	Mem     string
	MemPerc string
}

// ContainerSnapshotResult is returned by ContainerListSnapshot.
type ContainerSnapshotResult struct {
	Containers []ContainerEntry
	Err        error
}

// ContainerListSnapshot returns the current container list as a Go struct
// suitable for direct proto conversion, without JSON marshaling.
func ContainerListSnapshot(ctx context.Context) ContainerSnapshotResult {
	raw, err := containerListRaw(ctx)
	if err != nil {
		return ContainerSnapshotResult{Err: err}
	}
	entries := make([]ContainerEntry, 0, len(raw))
	for _, c := range raw {
		entries = append(entries, ContainerEntry{
			ID:      c.ID,
			Name:    c.Name,
			Image:   c.Image,
			State:   c.State,
			Status:  c.Status,
			CPU:     c.CPU,
			Mem:     c.Mem,
			MemPerc: c.MemPerc,
		})
	}
	return ContainerSnapshotResult{Containers: entries}
}

// containerListRaw queries the Docker daemon via the UNIX socket for all containers
// and collects live stats concurrently for running containers.
func containerListRaw(ctx context.Context) ([]containerInfo, error) {
	cli, err := docker.GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("docker list containers: %w", err)
	}

	result := make([]containerInfo, len(containers))
	var wg sync.WaitGroup

	for i, c := range containers {
		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}

		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		result[i] = containerInfo{
			ID:     id,
			Name:   name,
			Image:  c.Image,
			State:  c.State,
			Status: c.Status,
		}

		// Collect live stats for running containers
		if c.State == "running" {
			wg.Add(1)
			go func(idx int, containerID string) {
				defer wg.Done()

				statsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()

				resp, err := cli.ContainerStats(statsCtx, containerID, false)
				if err != nil {
					return
				}
				defer resp.Body.Close()

				statsJSON, err := docker.DecodeStats(resp.Body)
				if err != nil {
					return
				}

				cpuPercent := docker.CalculateCPUPercent(statsJSON)
				used, limit, memPerc := docker.CalculateMemUsageAndLimit(statsJSON)

				result[idx].CPU = docker.FormatPercent(cpuPercent)
				result[idx].Mem = docker.FormatMemString(used, limit)
				result[idx].MemPerc = docker.FormatPercent(memPerc)
			}(i, c.ID)
		}
	}

	wg.Wait()
	return result, nil
}

// containerList runs containerListRaw and returns a JSON array string.
func containerList(ctx context.Context) ExecuteResult {
	result, err := containerListRaw(ctx)
	if err != nil {
		return ExecuteResult{Err: err}
	}

	if len(result) == 0 {
		return ExecuteResult{Output: "[]"}
	}
	b, err := json.Marshal(result)
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("marshal containers: %w", err)}
	}
	return ExecuteResult{Output: string(b)}
}

// containerAction executes start/stop/restart directly on the Docker daemon.
func containerAction(ctx context.Context, action, name string) ExecuteResult {
	cli, err := docker.GetClient()
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("docker client: %w", err)}
	}

	stopTimeout := 15 // seconds
	switch action {
	case "start":
		if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
			return ExecuteResult{Err: fmt.Errorf("docker start %s: %w", name, err)}
		}
	case "stop":
		if err := cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &stopTimeout}); err != nil {
			return ExecuteResult{Err: fmt.Errorf("docker stop %s: %w", name, err)}
		}
	case "restart":
		if err := cli.ContainerRestart(ctx, name, container.StopOptions{Timeout: &stopTimeout}); err != nil {
			return ExecuteResult{Err: fmt.Errorf("docker restart %s: %w", name, err)}
		}
	default:
		return ExecuteResult{Err: fmt.Errorf("unsupported action %q", action)}
	}

	return ExecuteResult{Output: fmt.Sprintf("container %s %sed", name, action)}
}

// containerStats returns stats for a specific container in JSON format.
func containerStats(ctx context.Context, name string) ExecuteResult {
	cli, err := docker.GetClient()
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("docker client: %w", err)}
	}

	resp, err := cli.ContainerStats(ctx, name, false)
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("docker stats %s: %w", name, err)}
	}
	defer resp.Body.Close()

	statsJSON, err := docker.DecodeStats(resp.Body)
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("docker stats decode %s: %w", name, err)}
	}

	cpuPercent := docker.CalculateCPUPercent(statsJSON)
	used, limit, memPerc := docker.CalculateMemUsageAndLimit(statsJSON)

	type singleStats struct {
		Name    string `json:"name"`
		CPU     string `json:"cpu"`
		Mem     string `json:"mem"`
		MemPerc string `json:"memPerc"`
	}

	payload := singleStats{
		Name:    name,
		CPU:     docker.FormatPercent(cpuPercent),
		Mem:     docker.FormatMemString(used, limit),
		MemPerc: docker.FormatPercent(memPerc),
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("marshal stats %s: %w", name, err)}
	}
	return ExecuteResult{Output: string(out)}
}

// containerLogs retrieves the last 100 lines of logs using Docker Engine API,
// correctly demultiplexing streams with stdcopy.StdCopy for non-TTY containers.
func containerLogs(ctx context.Context, name string) ExecuteResult {
	cli, err := docker.GetClient()
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("docker client: %w", err)}
	}

	inspect, err := cli.ContainerInspect(ctx, name)
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("docker inspect %s: %w", name, err)}
	}

	logsReader, err := cli.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "100",
		Timestamps: false,
	})
	if err != nil {
		return ExecuteResult{Err: fmt.Errorf("docker logs %s: %w", name, err)}
	}
	defer logsReader.Close()

	if inspect.Config != nil && inspect.Config.Tty {
		out, err := io.ReadAll(logsReader)
		if err != nil {
			return ExecuteResult{Err: fmt.Errorf("read logs: %w", err)}
		}
		return ExecuteResult{Output: string(out)}
	}

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logsReader); err != nil && err != io.EOF {
		return ExecuteResult{Err: fmt.Errorf("demux logs %s: %w", name, err)}
	}

	return ExecuteResult{Output: stdout.String() + stderr.String()}
}
