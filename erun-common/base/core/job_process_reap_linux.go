//go:build linux

package core

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// StartEnvironmentJobChildReaper reaps zombies reparented onto this subreaper:
// nothing else waits on them, and signal 0 reports a zombie as alive. ownPID, the
// os/exec child, is never reaped here so Wait still gets its exit status (0: none).
// The returned stop function is idempotent.
func StartEnvironmentJobChildReaper(ownPID int) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGCHLD)

	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(stopped)
		for {
			// Sweep the whole table on every wake: signals coalesce, and a zombie may
			// predate the first one.
			reapEnvironmentJobAdoptedChildren(os.Getpid(), ownPID)
			select {
			case <-done:
				return
			case <-signals:
			}
		}
	}()
	return func() {
		once.Do(func() {
			signal.Stop(signals)
			close(done)
		})
		<-stopped
	}
}

// reapEnvironmentJobAdoptedChildren reaps every zombie child of selfPID except
// ownPID. It reads only the process table: spawning ps would race the reaper
// with its own child.
func reapEnvironmentJobAdoptedChildren(selfPID, ownPID int) {
	procs, ok := EnvironmentJobSessionProcessesFunc()
	if !ok {
		return
	}
	for _, proc := range procs {
		if proc.Pid <= 0 || !proc.Zombie || proc.Parent != selfPID {
			continue
		}
		if proc.Pid == ownPID {
			continue
		}
		ReapEnvironmentJobChild(proc.Pid)
	}
}

// ReapEnvironmentJobChild waits an exited child; ECHILD is ignored.
func ReapEnvironmentJobChild(pid int) {
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if err == syscall.EINTR {
			continue
		}
		return
	}
}
