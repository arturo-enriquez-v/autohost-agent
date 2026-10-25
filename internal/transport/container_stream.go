// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"io"
	"log"
	"time"

	"autohost-agent/internal/adapters/docker"
	"autohost-agent/internal/commands"
	pb "autohost-agent/internal/transport/nodepb"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

// ─── Container streaming ──────────────────────────────────────────────────────

// startContainerStream launches a goroutine that sends an initial
// ContainerListPayload snapshot and then watches Docker Engine events to resend on
// every container lifecycle change. Any previous stream is stopped first.
func (c *GRPCClient) startContainerStream(ctx context.Context, results chan<- *pb.NodeMessage) {
	c.stopContainerStream() // cancel any running stream

	streamCtx, cancel := context.WithCancel(ctx)

	c.containerMu.Lock()
	c.containerCancel = cancel
	c.containerMu.Unlock()

	go c.runContainerStream(streamCtx, cancel, results)
}

// stopContainerStream cancels the active container-streaming goroutine if any.
func (c *GRPCClient) stopContainerStream() {
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.containerCancel != nil {
		c.containerCancel()
		c.containerCancel = nil
	}
}

// runContainerStream is the goroutine body for container streaming.
// It sends an initial snapshot then keeps pushing updates:
//   - every periodicInterval (ticker) to refresh CPU/mem stats, and
//   - whenever a Docker lifecycle event occurs (start, stop, die, …).
func cGRPCRunContainerStream(c *GRPCClient, ctx context.Context, cancel context.CancelFunc, out chan<- *pb.NodeMessage) {
	defer cancel()

	// Initial snapshot.
	c.sendContainerSnapshot(ctx, out)

	const debounce = 500 * time.Millisecond
	debounceTimer := time.NewTimer(debounce)
	debounceTimer.Stop() // starts paused
	defer debounceTimer.Stop()

	// Periodic ticker so CPU/mem stats refresh even when no lifecycle events occur.
	const periodicInterval = 10 * time.Second
	ticker := time.NewTicker(periodicInterval)
	defer ticker.Stop()

	pending := false

	// eventSig is written to by watchDockerEvents. It is NEVER closed,
	// so the select loop never sees a !ok case and never exits.
	eventSig := make(chan struct{}, 1)
	go c.watchDockerEvents(ctx, eventSig)

	for {
		select {
		case <-ctx.Done():
			return

		case <-eventSig:
			if !pending {
				pending = true
				debounceTimer.Reset(debounce)
			}

		case <-debounceTimer.C:
			pending = false
			c.sendContainerSnapshot(ctx, out)

		case <-ticker.C:
			c.sendContainerSnapshot(ctx, out)
		}
	}
}

func (c *GRPCClient) runContainerStream(ctx context.Context, cancel context.CancelFunc, out chan<- *pb.NodeMessage) {
	cGRPCRunContainerStream(c, ctx, cancel, out)
}

// watchDockerEvents listens to the Docker Engine API event stream via UNIX socket,
// writing to sig on every container lifecycle event. It reconnects with exponential
// back-off on transient disconnects.
func (c *GRPCClient) watchDockerEvents(ctx context.Context, sig chan<- struct{}) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		cli, err := docker.GetClient()
		if err != nil {
			log.Printf("⚠️  container stream: get docker client: %v", err)
			if !sleepWithBackoff(ctx, &backoff, maxBackoff) {
				return
			}
			continue
		}

		eventFilters := filters.NewArgs(
			filters.Arg("type", "container"),
			filters.Arg("event", "start"),
			filters.Arg("event", "die"),
			filters.Arg("event", "stop"),
			filters.Arg("event", "pause"),
			filters.Arg("event", "unpause"),
			filters.Arg("event", "create"),
			filters.Arg("event", "destroy"),
			filters.Arg("event", "rename"),
		)

		eventCtx, eventCancel := context.WithCancel(ctx)
		msgChan, errChan := cli.Events(eventCtx, events.ListOptions{Filters: eventFilters})

		hadEvents := false
	eventLoop:
		for {
			select {
			case <-ctx.Done():
				eventCancel()
				return

			case _, ok := <-msgChan:
				if !ok {
					break eventLoop
				}
				hadEvents = true
				select {
				case sig <- struct{}{}:
				default: // already has a pending signal — skip
				}

			case err, ok := <-errChan:
				if !ok || err == nil {
					break eventLoop
				}
				if ctx.Err() == nil && err != io.EOF && err != context.Canceled {
					log.Printf("⚠️  container stream: docker events error: %v", err)
				}
				break eventLoop
			}
		}

		eventCancel()

		if ctx.Err() != nil {
			return
		}

		if hadEvents {
			backoff = time.Second // reset backoff if it had successfully received events
		}

		if !sleepWithBackoff(ctx, &backoff, maxBackoff) {
			return
		}
	}
}

// sleepWithBackoff waits for the current backoff duration (respecting ctx
// cancellation), then doubles it up to maxBackoff. Returns false if ctx was
// cancelled during the wait.
func sleepWithBackoff(ctx context.Context, backoff *time.Duration, maxBackoff time.Duration) bool {
	select {
	case <-time.After(*backoff):
	case <-ctx.Done():
		return false
	}
	*backoff *= 2
	if *backoff > maxBackoff {
		*backoff = maxBackoff
	}
	return true
}

// sendContainerSnapshot calls ContainerListSnapshot and pushes the result to out.
func (c *GRPCClient) sendContainerSnapshot(ctx context.Context, out chan<- *pb.NodeMessage) {
	result := commands.ContainerListSnapshot(ctx)

	payload := &pb.ContainerListPayload{}
	if result.Err != nil {
		payload.Error = result.Err.Error()
	}
	for _, ci := range result.Containers {
		payload.Containers = append(payload.Containers, &pb.ContainerInfo{
			Id:      ci.ID,
			Name:    ci.Name,
			Image:   ci.Image,
			State:   ci.State,
			Status:  ci.Status,
			Cpu:     ci.CPU,
			Mem:     ci.Mem,
			MemPerc: ci.MemPerc,
		})
	}

	msg := &pb.NodeMessage{
		Payload: &pb.NodeMessage_ContainerList{
			ContainerList: payload,
		},
	}
	select {
	case out <- msg:
	case <-ctx.Done():
	default:
		log.Printf("⚠️  gRPC: container snapshot buffer full, skipping")
	}
}
