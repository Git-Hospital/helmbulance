//go:build unix

package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The first Ctrl-C cancels the run so cleanup can start; a second one must quit at once, since
// cleanup can take minutes. Signals go to the whole test binary, so this runs in a child process.
func TestInterruptContext(t *testing.T) {
	if os.Getenv("HB_INTERRUPT_CHILD") == "1" {
		ctx, cancel := interruptContext()
		defer cancel()
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			os.Exit(3) // the first Ctrl-C did not cancel
		}
		time.Sleep(100 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		time.Sleep(5 * time.Second)
		os.Exit(4) // the second Ctrl-C did not quit
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestInterruptContext$")
	cmd.Env = append(os.Environ(), "HB_INTERRUPT_CHILD=1")
	err := cmd.Run()
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("child ended with %v, want killed by the second SIGINT (exit 3: first did not cancel, 4: second did not quit)", err)
	}
}
