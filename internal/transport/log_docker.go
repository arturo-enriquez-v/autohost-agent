// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"time"

	pb "autohost-agent/internal/grpc/nodepb"
	"autohost-agent/internal/infra/docker"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// streamDockerLogs tails container logs via Docker Engine API over UNIX socket
// and forwards each line over gRPC. Both stdout and stderr are demultiplexed
// and merged into a single stream.
func (c *GRPCClient) streamDockerLogs(logCtx context.Context, cancel context.CancelFunc, containerName string, histLines int32, results chan<- *pb.NodeMessage) {
	defer cancel()

	sendMsg := func(msg string) {
		select {
		case results <- &pb.NodeMessage{
			Payload: &pb.NodeMessage_LogEntry{LogEntry: &pb.LogEntryPayload{
				TimestampUs: time.Now().UnixMicro(),
				Unit:        "docker:" + containerName,
				Message:     msg,
			}},
		}:
		case <-logCtx.Done():
		}
	}

	cli, err := docker.GetClient()
	if err != nil {
		log.Printf("⚠️  docker log stream client error: %v", err)
		sendMsg("⚠️ docker client error: " + err.Error())
		return
	}

	firstRun := true
	for {
		if logCtx.Err() != nil {
			return
		}

		opts := container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
			Timestamps: true,
		}
		if firstRun && histLines > 0 {
			opts.Tail = fmt.Sprintf("%d", histLines)
		} else {
			opts.Tail = "0"
		}

		iterCtx, iterCancel := context.WithCancel(logCtx)

		logsReader, err := cli.ContainerLogs(iterCtx, containerName, opts)
		if err != nil {
			iterCancel()
			log.Printf("⚠️  docker logs start error: %v", err)
			sendMsg("⚠️ docker logs start error: " + err.Error())
			return
		}

		log.Printf("📋 docker log stream started (container=%q lines=%d firstRun=%v)", containerName, histLines, firstRun)
		firstRun = false

		// Check if container has TTY enabled
		isTTY := false
		if inspect, inspErr := cli.ContainerInspect(iterCtx, containerName); inspErr == nil && inspect.Config != nil {
			isTTY = inspect.Config.Tty
		}

		var lineReader io.Reader
		var cleanupReader func()

		if isTTY {
			lineReader = logsReader
			cleanupReader = func() {
				logsReader.Close()
				iterCancel()
			}
		} else {
			pr, pw := io.Pipe()
			go func() {
				_, _ = stdcopy.StdCopy(pw, pw, logsReader)
				_ = pw.Close()
			}()
			lineReader = pr
			cleanupReader = func() {
				_ = logsReader.Close()
				_ = pr.Close()
				_ = pw.Close()
				iterCancel()
			}
		}

		scanner := bufio.NewScanner(lineReader)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			line = sanitizeLogLine(line)
			select {
			case results <- &pb.NodeMessage{
				Payload: &pb.NodeMessage_LogEntry{LogEntry: &pb.LogEntryPayload{
					TimestampUs: time.Now().UnixMicro(),
					Unit:        "docker:" + containerName,
					Message:     line,
				}},
			}:
			case <-logCtx.Done():
				cleanupReader()
				return
			default:
				// drop if send buffer is full
			}
		}

		cleanupReader()

		if logCtx.Err() != nil {
			log.Printf("📋 docker log stream stopped (container=%q)", containerName)
			return
		}

		log.Printf("⚠️  docker logs stream disconnected (container=%q), retrying in 3s…", containerName)
		sendMsg("⚠️ docker logs stream disconnected, retrying in 3s…")
		select {
		case <-logCtx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
