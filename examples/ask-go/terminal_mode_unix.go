//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package main

import (
	"fmt"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

func makeInputCancelMode(fd uintptr) (*terminalModeState, error) {
	oldState, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	next := *oldState
	next.Termios.Lflag &^= unix.ECHO | unix.ICANON
	next.Termios.Cc[unix.VMIN] = 0
	next.Termios.Cc[unix.VTIME] = 1
	if err := term.SetState(fd, &next); err != nil {
		return nil, err
	}
	return &terminalModeState{value: oldState}, nil
}

func restoreInputCancelMode(fd uintptr, state *terminalModeState) error {
	if state == nil || state.value == nil {
		return nil
	}
	oldState, ok := state.value.(*term.State)
	if !ok {
		return fmt.Errorf("unexpected terminal state type %T", state.value)
	}
	return term.Restore(fd, oldState)
}
