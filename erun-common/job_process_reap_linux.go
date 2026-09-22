//go:build linux

package eruncommon

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// startEnvironmentJobChildReaper reaps the processes that reparent onto this
// process because it is a child subreaper (see enableEnvironmentJobSubreaper),
// and that are not the one os/exec child it started for itself.
//
// A child subreaper claims every descendant of it that gets orphaned, so the
// supervisor inherits whatever a job's work leaves behind -- an agent tool's
// backgrounded Bash command, an intermediate wrapper that died before it could
// wait() on its own child. Nothing reaps those but this: they are not os/exec
// children, so no Wait is coming for them, and a subreaper that never reaps
// them is not an init, it is a graveyard. They then sit as zombies under the
// supervisor's pid for as long as it lives, and signal 0 -- the "does this pid
// exist" probe this codebase uses for liveness, including for lease holders --
// answers true for a zombie. An exited process reading as alive is a false
// success wherever that probe is the one asked.
//
// ownPID is the pid of the job's own command, the os/exec child this supervisor
// is waiting on, and is never reaped here: os/exec's Wait is the only thing
// that can report that child's exit status, and a reaper that consumed it first
// would turn a recorded exit code into "no child processes". Passing 0 says
// there is no such child to protect.
//
// It returns a stop function that is safe to call more than once.
func startEnvironmentJobChildReaper(ownPID int) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGCHLD)

	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(stopped)
		for {
			// Sweep before waiting, so a child that was already a zombie when
			// this started -- or one that arrived while the last sweep ran --
			// is reaped on the next signal rather than never. Signals coalesce
			// and a full channel drops them, which is why the sweep answers
			// every wake with the whole table rather than one pid: a burst
			// that arrives as a single signal still reaps every member of it.
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

// reapEnvironmentJobAdoptedChildren reaps every zombie whose parent is selfPID,
// other than ownPID.
//
// Only the platform's own process table is consulted, never the `ps` fallback
// the survivor checks fall back to: spawning a helper from inside the reaper
// would make that helper one of this process's children, and the reaper would
// then be racing its own probe. A host with no table to read simply reaps
// nothing, which is the same best-effort answer enableEnvironmentJobSubreaper
// gives when the kernel refuses the attribute.
//
// A zombie is the only thing considered. A live reparented descendant is work
// this job still has running, which is the survivor checks' question to answer,
// not this one's to reap.
func reapEnvironmentJobAdoptedChildren(selfPID, ownPID int) {
	procs, ok := environmentJobSessionProcessesFunc()
	if !ok {
		return
	}
	for _, proc := range procs {
		if proc.pid <= 0 || !proc.zombie || proc.parent != selfPID {
			continue
		}
		if proc.pid == ownPID {
			continue
		}
		reapEnvironmentJobChild(proc.pid)
	}
}

// reapEnvironmentJobChild waits a child that has already exited, so the kernel
// releases it. It is best-effort by design: an ECHILD answer means someone else
// already reaped it, and there is nothing left to do either way.
func reapEnvironmentJobChild(pid int) {
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if err == syscall.EINTR {
			continue
		}
		return
	}
}
