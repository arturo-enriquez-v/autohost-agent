//go:build linux

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package transport

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

type ptyWinsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func openPTY(cols, rows uint32) (*os.File, *os.File, error) {
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

func configureCmdPTY(cmd *exec.Cmd, tty *os.File) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    int(tty.Fd()),
	}
}

func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
