// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	pb "autohost-agent/internal/grpc/nodepb"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// streamJournalctlLogs tails journalctl and forwards each line.
func (c *GRPCClient) streamJournalctlLogs(logCtx context.Context, cancel context.CancelFunc, unit string, histLines int32, results chan<- *pb.NodeMessage) {
	defer cancel()

	sendMsg := func(u, msg string) {
		select {
		case results <- &pb.NodeMessage{
			Payload: &pb.NodeMessage_LogEntry{LogEntry: &pb.LogEntryPayload{
				TimestampUs: time.Now().UnixMicro(),
				Unit:        u,
				Message:     msg,
			}},
		}:
		case <-logCtx.Done():
		}
	}

	// firstRun controls tail lines: show history on first launch, skip on retries.
	firstRun := true
	for {
		if logCtx.Err() != nil {
			return
		}

		args := []string{"-f", "--output=short-iso", "--no-pager"}
		if firstRun && histLines > 0 {
			args = append(args, "-n", fmt.Sprintf("%d", histLines))
		} else {
			args = append(args, "-n", "0")
		}
		if unit != "" {
			args = append(args, "-u", unit)
		}

		cmd := exec.CommandContext(logCtx, "journalctl", args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("⚠️  journalctl pipe: %v", err)
			sendMsg("agent", "⚠️ journalctl pipe error: "+err.Error())
			return
		}
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf

		if err := cmd.Start(); err != nil {
			log.Printf("⚠️  journalctl start: %v", err)
			sendMsg("agent", "⚠️ journalctl start error: "+err.Error())
			return
		}
		log.Printf("📋 log stream started (unit=%q lines=%d firstRun=%v)", unit, histLines, firstRun)
		firstRun = false

		// iterCtx is cancelled when either this iteration ends or the outer
		// logCtx is cancelled. Using a per-iteration context prevents goroutine
		// accumulation across retries (e.g. when journalctl repeatedly exits).
		iterCtx, iterCancel := context.WithCancel(logCtx)

		// Unblock scanner.Scan() immediately when this iteration ends,
		// without waiting for the exec.CommandContext → cmd.Wait chain.
		go func() {
			<-iterCtx.Done()
			stdout.Close()
		}()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			line = sanitizeLogLine(line)
			u := unit
			if u == "" {
				u = extractUnit(line)
			}
			select {
			case results <- &pb.NodeMessage{
				Payload: &pb.NodeMessage_LogEntry{LogEntry: &pb.LogEntryPayload{
					TimestampUs: time.Now().UnixMicro(),
					Unit:        u,
					Message:     line,
				}},
			}:
			case <-logCtx.Done():
				iterCancel()
				cmd.Process.Kill()
				return
			default:
				// drop if the send buffer is full
			}
		}
		cmd.Wait()
		iterCancel() // ensure the stdout-close goroutine exits before next iteration

		if errOut := strings.TrimSpace(stderrBuf.String()); errOut != "" {
			log.Printf("⚠️  journalctl stderr: %s", errOut)
			sendMsg("agent", "⚠️ journalctl: "+errOut)
		}

		// If context was cancelled (stream stopped intentionally), exit cleanly.
		if logCtx.Err() != nil {
			log.Printf("📋 log stream stopped (unit=%q)", unit)
			return
		}

		// journalctl exited unexpectedly (e.g. journal not ready in container).
		// Wait briefly and retry so the UI keeps receiving logs once available.
		log.Printf("⚠️  journalctl exited unexpectedly (unit=%q), retrying in 3s…", unit)
		sendMsg("agent", "⚠️ journalctl exited, retrying in 3s…")
		select {
		case <-logCtx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// extractUnit tries to parse the systemd unit name from a journalctl line.
// journalctl --output=short-iso lines look like:
//
//	2026-03-09T12:00:00+0000 hostname unit[pid]: message
func extractUnit(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 3 {
		u := parts[2]
		// Strip "[pid]:" suffix if present
		if idx := strings.IndexByte(u, '['); idx > 0 {
			return u[:idx]
		}
		if strings.HasSuffix(u, ":") {
			return u[:len(u)-1]
		}
		return u
	}
	return ""
}
