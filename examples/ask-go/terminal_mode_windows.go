//go:build windows

package main

import "fmt"

func makeInputCancelMode(fd uintptr) (*terminalModeState, error) {
	return nil, fmt.Errorf("interactive escape cancel is not supported on windows")
}

func restoreInputCancelMode(fd uintptr, state *terminalModeState) error {
	return nil
}
