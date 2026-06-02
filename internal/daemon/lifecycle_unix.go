//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// processAlive checks if a process with the given PID exists.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 doesn't kill the process; it just checks if it exists.
	return proc.Signal(syscall.Signal(0)) == nil
}
