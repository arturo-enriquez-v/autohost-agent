package transport

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "autohost-agent/internal/transport/nodepb"
)

func TestTerminalManager_StartSession(t *testing.T) {
	mgr := newTerminalManager()
	out := make(chan *pb.NodeMessage, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := &pb.StartTerminalPayload{
		SessionId: "test-sess-1",
		Cols:      80,
		Rows:      24,
	}

	mgr.startSession(ctx, req, out)

	// Write input command
	time.Sleep(100 * time.Millisecond)
	mgr.writeInput(&pb.TerminalInputPayload{
		SessionId: "test-sess-1",
		Data:      []byte("echo 'TEST_OK_PTY'\n"),
	})

	var outputReceived strings.Builder
	timeout := time.After(3 * time.Second)
	for {
		select {
		case msg := <-out:
			if outMsg := msg.GetTerminalOutput(); outMsg != nil {
				outputReceived.Write(outMsg.GetData())
				if strings.Contains(outputReceived.String(), "TEST_OK_PTY") {
					t.Logf("Successfully executed command and got echo back!")
					return
				}
			}
			if closedMsg := msg.GetTerminalClosed(); closedMsg != nil {
				t.Fatalf("Session closed prematurely! ExitCode: %d, Error: %s", closedMsg.GetExitCode(), closedMsg.GetError())
			}
		case <-timeout:
			t.Fatalf("Timed out waiting for TEST_OK_PTY output. Got: %q", outputReceived.String())
		}
	}
}
