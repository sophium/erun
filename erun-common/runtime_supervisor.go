package eruncommon

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// runtimeSupervisor owns every process the runtime entrypoint starts and the
// loops that keep its services running. As PID 1 it is also the parent of every
// orphan in the container, so it tells the two apart: a process it started is
// waited by the goroutine that started it (os/exec's Wait is the only thing that
// can report that exit status), and every other child that turns into a zombie
// is reaped by the init loop (runtime_supervisor_linux.go).
type runtimeSupervisor struct {
	ctx core.Context

	mu    sync.Mutex
	owned map[int]*exec.Cmd
	// foreground is the container's own command in command mode; signals are
	// forwarded to it and its exit code is the container's.
	foreground *exec.Cmd

	done     chan struct{}
	doneOnce sync.Once
	loops    sync.WaitGroup

	// terminated holds a terminating signal that arrived with no foreground
	// command: awaitTermination reads it in devops mode, runForeground forwards
	// it to the command in command mode.
	terminated chan os.Signal
}

// runtimeShutdownGrace bounds how long the entrypoint waits for what it started
// to exit after forwarding a termination signal, before killing it. It stays
// well inside the kubelet's default 30s termination grace.
const runtimeShutdownGrace = 10 * time.Second

var errRuntimeSupervisorStopping = errors.New("the runtime entrypoint is shutting down")

func newRuntimeSupervisor(ctx core.Context) *runtimeSupervisor {
	return &runtimeSupervisor{
		ctx:        ctx,
		owned:      map[int]*exec.Cmd{},
		done:       make(chan struct{}),
		terminated: make(chan os.Signal, 1),
	}
}

func (s *runtimeSupervisor) stopping() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// start starts cmd and records it as owned in one critical section, so the
// orphan reaper can never see it exit before it is known to be ours.
func (s *runtimeSupervisor) start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked(cmd)
}

func (s *runtimeSupervisor) startLocked(cmd *exec.Cmd) error {
	if s.stopping() {
		return errRuntimeSupervisorStopping
	}
	core.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.owned[cmd.Process.Pid] = cmd
	return nil
}

func (s *runtimeSupervisor) wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	s.mu.Lock()
	delete(s.owned, cmd.Process.Pid)
	s.mu.Unlock()
	return err
}

func (s *runtimeSupervisor) run(cmd *exec.Cmd) error {
	if err := s.start(cmd); err != nil {
		return err
	}
	return s.wait(cmd)
}

// goLoop runs body until it returns false; body returns false once the
// supervisor is stopping.
func (s *runtimeSupervisor) goLoop(body func() bool) {
	s.loops.Add(1)
	go func() {
		defer s.loops.Done()
		for body() {
		}
	}()
}

// sleep waits d, returning false as soon as the supervisor is stopping.
func (s *runtimeSupervisor) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-s.done:
		return false
	case <-timer.C:
		return true
	}
}

func (s *runtimeSupervisor) ownedProcesses() []*os.Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	processes := make([]*os.Process, 0, len(s.owned))
	for _, cmd := range s.owned {
		processes = append(processes, cmd.Process)
	}
	return processes
}

func (s *runtimeSupervisor) signalOwned(sig os.Signal) {
	for _, process := range s.ownedProcesses() {
		_ = process.Signal(sig)
	}
}

func (s *runtimeSupervisor) ownedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.owned)
}

// shutdown stops every loop and asks every owned process to exit with sig,
// killing what is still running after runtimeShutdownGrace.
func (s *runtimeSupervisor) shutdown(sig os.Signal) {
	s.doneOnce.Do(func() { close(s.done) })
	s.signalOwned(sig)
	deadline := time.Now().Add(runtimeShutdownGrace)
	for s.ownedCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	s.signalOwned(os.Kill)
	loopsDone := make(chan struct{})
	go func() {
		s.loops.Wait()
		close(loopsDone)
	}()
	select {
	case <-loopsDone:
	case <-time.After(runtimeShutdownGrace):
	}
}

// runtimeTerminatingSignals end the container: in devops mode they stop the
// boot's own wait, in command mode they are forwarded and the container's
// command decides.
var runtimeTerminatingSignals = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT}

// startInit makes this process the container's init. On Linux every orphan
// reparents here (PID 1 in the container; a child subreaper anywhere else, so a
// test run behaves the same) and zombies are reaped as SIGCHLD arrives; on every
// platform signals are forwarded to what this process started. The returned
// stop shuts every owned process down; it is safe to call more than once.
func (s *runtimeSupervisor) startInit() func() {
	enableRuntimeSubreaper()
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, append(append([]os.Signal{}, runtimeInitSignals...), runtimeTerminatingSignals...)...)
	stopped := make(chan struct{})
	go func() {
		for {
			// Sweep before waiting so a zombie that existed before the first
			// signal, or arrived during the last sweep, is not missed: SIGCHLD
			// coalesces, so every wake reaps the whole table.
			s.reapOrphans()
			select {
			case <-stopped:
				return
			case sig := <-signals:
				if !isRuntimeReapSignal(sig) {
					s.handleSignal(sig)
				}
			}
		}
	}()
	return func() {
		select {
		case <-stopped:
			return
		default:
		}
		signal.Stop(signals)
		s.shutdown(syscall.SIGTERM)
		close(stopped)
	}
}

func (s *runtimeSupervisor) handleSignal(sig os.Signal) {
	s.mu.Lock()
	foreground := s.foreground
	if foreground == nil && isRuntimeTerminatingSignal(sig) {
		// Parked under the lock runForeground registers its command under, so
		// a stop signal is either seen here with the command registered or
		// parked before runForeground drains it: never lost between the two.
		select {
		case s.terminated <- sig:
		default:
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if foreground != nil {
		// Command mode: the container's command decides what a signal means.
		_ = foreground.Process.Signal(sig)
		if isRuntimeTerminatingSignal(sig) {
			s.signalOwned(sig)
		}
		return
	}
	s.signalOwned(sig)
}

func isRuntimeTerminatingSignal(sig os.Signal) bool {
	for _, candidate := range runtimeTerminatingSignals {
		if sig == candidate {
			return true
		}
	}
	return false
}

// awaitTermination blocks until a terminating signal arrives, shuts everything
// down, and returns the exit code an init reports for it: 128 + the signal.
func (s *runtimeSupervisor) awaitTermination() int {
	sig := <-s.terminated
	s.ctx.Info("erun: runtime-init: received " + sig.String() + "; stopping")
	s.shutdown(sig)
	if number, ok := sig.(syscall.Signal); ok {
		return 128 + int(number)
	}
	return 1
}

// runForeground runs the container's command with the container's stdio and
// returns its exit code, or 128 + the signal that killed it. A stop signal
// that arrived before the command was registered (during the preamble) is
// forwarded to it as soon as it starts, as tini would have.
func (s *runtimeSupervisor) runForeground(command []string) (int, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	s.mu.Lock()
	err := s.startLocked(cmd)
	var pending os.Signal
	if err == nil {
		s.foreground = cmd
		select {
		case pending = <-s.terminated:
		default:
		}
	}
	s.mu.Unlock()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return 127, err
		}
		return 126, err
	}
	if pending != nil {
		_ = cmd.Process.Signal(pending)
		s.signalOwned(pending)
	}
	err = s.wait(cmd)
	return runtimeExitCode(cmd, err), nil
}
