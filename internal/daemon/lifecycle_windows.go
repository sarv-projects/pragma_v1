//go:build windows

package daemon

import (
	"golang.org/x/sys/windows"
)

// processAlive checks if a process with the given PID exists.
func processAlive(pid int) bool {
	// Query limited information to check process existence without requiring full access
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	err = windows.GetExitCodeProcess(h, &exitCode)
	if err != nil {
		return false
	}

	// 259 is STILL_ACTIVE
	return exitCode == 259
}
