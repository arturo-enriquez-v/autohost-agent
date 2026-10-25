// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"strings"

	pb "autohost-agent/internal/transport/nodepb"
)

const (
	// MaxHistoryLines is the maximum number of past log lines that can be requested in a single stream.
	MaxHistoryLines int32 = 1000

	// MaxLogLineBytes is the maximum allowed byte length for a single log line before truncation.
	MaxLogLineBytes int = 4096
)

// clampHistoryLines restricts requested history lines to safe bounds [0, 1000].
func clampHistoryLines(lines int32) int32 {
	if lines <= 0 {
		return 0
	}
	if lines > MaxHistoryLines {
		return MaxHistoryLines
	}
	return lines
}

// sanitizeLogLine truncates oversized log lines to prevent memory exhaustion and DoS.
func sanitizeLogLine(line string) string {
	if len(line) > MaxLogLineBytes {
		return line[:MaxLogLineBytes] + " ... [truncated]"
	}
	return line
}

// ─── Log streaming ────────────────────────────────────────────────────────────

// startLogStream launches a goroutine that streams logs and sends each line as
// a LogEntryPayload through the results channel.
//
// If the unit value starts with the "docker:" prefix the goroutine runs
// "docker logs -f <container>" instead of journalctl. Any other value (or an
// empty string) falls back to journalctl.
func (c *GRPCClient) startLogStream(ctx context.Context, req *pb.StreamLogsPayload, results chan<- *pb.NodeMessage) {
	// Grab cancel + done for the currently running goroutine (if any).
	c.logMu.Lock()
	prevCancel := c.logCancel
	prevDone := c.logDone
	c.logMu.Unlock()

	// Cancel the previous stream and wait for its goroutine to fully exit
	// before allocating a new context, so we never overlap two log processes.
	if prevCancel != nil {
		prevCancel()
	}
	if prevDone != nil {
		<-prevDone
	}

	logCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	c.logMu.Lock()
	c.logCancel = cancel
	c.logDone = done
	c.logMu.Unlock()

	unit := req.GetUnit()
	safeLines := clampHistoryLines(req.GetLines())

	if strings.HasPrefix(unit, "docker:") {
		containerName := strings.TrimPrefix(unit, "docker:")
		go func() {
			defer close(done)
			c.streamDockerLogs(logCtx, cancel, containerName, safeLines, results)
		}()
	} else {
		go func() {
			defer close(done)
			c.streamJournalctlLogs(logCtx, cancel, unit, safeLines, results)
		}()
	}
}

// stopLogStream cancels the active log goroutine (if any).
func (c *GRPCClient) stopLogStream() {
	c.logMu.Lock()
	cancel := c.logCancel
	c.logCancel = nil
	c.logMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
