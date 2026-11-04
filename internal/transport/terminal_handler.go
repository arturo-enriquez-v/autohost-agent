// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"autohost-agent/internal/platform/paths"
	pb "autohost-agent/internal/transport/nodepb"
)

type ptyWinsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

type activeTerminalSession struct {
	sessionID string
	ptmx      *os.File
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	closeOnce sync.Once
}

type terminalManager struct {
	mu       sync.RWMutex
	sessions map[string]*activeTerminalSession
}

func newTerminalManager() *terminalManager {
	return &terminalManager{
		sessions: make(map[string]*activeTerminalSession),
	}
}

func openLinuxPTY(cols, rows uint32) (*os.File, *os.File, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	var ptyno uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptyno))); errno != 0 {
		_ = ptmx.Close()
		return nil, nil, fmt.Errorf("TIOCGPTN: %w", errno)
	}

	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = ptmx.Close()
		return nil, nil, fmt.Errorf("TIOCSPTLCK: %w", errno)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptyno)
	tty, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = ptmx.Close()
		return nil, nil, fmt.Errorf("open %s: %w", slavePath, err)
	}

	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	setPTYWindowSize(ptmx, cols, rows)

	return ptmx, tty, nil
}

func setPTYWindowSize(ptmx *os.File, cols, rows uint32) {
	if ptmx == nil {
		return
	}
	ws := &ptyWinsize{
		Row: uint16(rows),
		Col: uint16(cols),
	}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
}

func resolveShellBinary() string {
	for _, candidate := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return "/bin/sh"
}

func (m *terminalManager) startSession(ctx context.Context, req *pb.StartTerminalPayload, out chan<- *pb.NodeMessage) {
	sessionID := req.GetSessionId()
	if sessionID == "" {
		return
	}

	// Close any existing session with the same ID first.
	m.closeSession(sessionID)

	ptmx, tty, err := openLinuxPTY(req.GetCols(), req.GetRows())
	if err != nil {
		log.Printf("⚠️  terminal: failed to allocate PTY for session %s: %v", sessionID, err)
		sendTerminalClosed(out, sessionID, 1, fmt.Sprintf("failed to allocate PTY: %v", err))
		return
	}

	shell := resolveShellBinary()
	sessCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(sessCtx, shell, "-i")

	sharedDir := paths.GetSharedDir()
	_ = os.MkdirAll(sharedDir, 0755)
	if info, err := os.Stat(sharedDir); err == nil && info.IsDir() {
		cmd.Dir = sharedDir
	} else {
		cmd.Dir = "/"
	}

	homeDir := os.Getenv("HOME")
	if homeDir == "" || homeDir == "/" {
		homeDir = paths.GetAutohostDir()
	}

	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"HOME="+homeDir,
		"AUTOHOST_SHARED_DIR="+sharedDir,
	)
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		_ = ptmx.Close()
		cancel()
		log.Printf("⚠️  terminal: failed to start shell %s for session %s: %v", shell, sessionID, err)
		sendTerminalClosed(out, sessionID, 1, fmt.Sprintf("failed to start shell: %v", err))
		return
	}
	// Close child side of the PTY in parent process so EOF is detected when shell exits.
	_ = tty.Close()

	sess := &activeTerminalSession{
		sessionID: sessionID,
		ptmx:      ptmx,
		cmd:       cmd,
		cancel:    cancel,
	}

	m.mu.Lock()
	m.sessions[sessionID] = sess
	m.mu.Unlock()

	log.Printf("🖥️  terminal: session %s started (shell=%s, cols=%d, rows=%d)", sessionID, shell, req.GetCols(), req.GetRows())

	// Read loop from PTY master -> gRPC stream
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case out <- &pb.NodeMessage{
					Payload: &pb.NodeMessage_TerminalOutput{
						TerminalOutput: &pb.TerminalOutputPayload{
							SessionId: sessionID,
							Data:      chunk,
						},
					},
				}:
				case <-sessCtx.Done():
					return
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					log.Printf("ℹ️  terminal session %s read ended: %v", sessionID, readErr)
				}
				break
			}
		}
	}()

	// Wait for process exit and notify server
	go func() {
		waitErr := cmd.Wait()
		exitCode := int32(0)
		reason := "exited"
		if waitErr != nil {
			reason = waitErr.Error()
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = int32(exitErr.ExitCode())
			} else {
				exitCode = 1
			}
		}

		m.closeSession(sessionID)
		sendTerminalClosed(out, sessionID, exitCode, reason)
		log.Printf("🖥️  terminal: session %s closed (exit=%d)", sessionID, exitCode)
	}()
}

func (m *terminalManager) writeInput(req *pb.TerminalInputPayload) {
	if req == nil || len(req.GetData()) == 0 {
		return
	}
	m.mu.RLock()
	sess, ok := m.sessions[req.GetSessionId()]
	m.mu.RUnlock()
	if !ok || sess.ptmx == nil {
		return
	}
	_, _ = sess.ptmx.Write(req.GetData())
}

func (m *terminalManager) resizeSession(req *pb.TerminalResizePayload) {
	if req == nil {
		return
	}
	m.mu.RLock()
	sess, ok := m.sessions[req.GetSessionId()]
	m.mu.RUnlock()
	if !ok || sess.ptmx == nil {
		return
	}
	setPTYWindowSize(sess.ptmx, req.GetCols(), req.GetRows())
}

func (m *terminalManager) closeSession(sessionID string) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if !ok || sess == nil {
		return
	}
	sess.closeOnce.Do(func() {
		sess.cancel()
		if sess.ptmx != nil {
			_ = sess.ptmx.Close()
		}
		if sess.cmd != nil && sess.cmd.Process != nil {
			_ = syscall.Kill(-sess.cmd.Process.Pid, syscall.SIGKILL)
			_ = sess.cmd.Process.Kill()
		}
	})
}

func (m *terminalManager) stopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.closeSession(id)
	}
}

func sendTerminalClosed(out chan<- *pb.NodeMessage, sessionID string, exitCode int32, reason string) {
	select {
	case out <- &pb.NodeMessage{
		Payload: &pb.NodeMessage_TerminalClosed{
			TerminalClosed: &pb.TerminalClosedPayload{
				SessionId: sessionID,
				ExitCode:  exitCode,
				Error:     reason,
			},
		},
	}:
	default:
	}
}
