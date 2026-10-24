package podruntime

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

// runtimeSupervisor waits on processes it started (only os/exec's Wait can report their
// status); the init loop reaps every other zombie.
type runtimeSupervisor struct {
	ctx core.Context

	mu         sync.Mutex
	owned      map[int]*exec.Cmd
	foreground *exec.Cmd

	done     chan struct{}
	doneOnce sync.Once
	loops    sync.WaitGroup

	// terminated holds a terminating signal that arrived with no foreground command.
	terminated chan os.Signal
}

// runtimeShutdownGrace stays well inside the kubelet's default 30s termination grace.
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

// start records cmd as owned in the same critical section, so the reaper never sees it
// exit unowned.
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

func (s *runtimeSupervisor) goLoop(body func() bool) {
	s.loops.Add(1)
	go func() {
		defer s.loops.Done()
		for body() {
		}
	}()
}

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

var runtimeTerminatingSignals = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT}

// startInit also enables a child subreaper when not PID 1, so tests behave like the
// container. The returned stop is safe to call more than once.
func (s *runtimeSupervisor) startInit() func() {
	enableRuntimeSubreaper()
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, append(append([]os.Signal{}, runtimeInitSignals...), runtimeTerminatingSignals...)...)
	stopped := make(chan struct{})
	go func() {
		for {
			// Sweep before waiting: SIGCHLD coalesces, so every wake reaps the whole table.
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
		// Parked under runForeground's lock so a stop signal is never lost between the two.
		select {
		case s.terminated <- sig:
		default:
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if foreground != nil {
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

func (s *runtimeSupervisor) awaitTermination() int {
	sig := <-s.terminated
	s.ctx.Info("erun: runtime-init: received " + sig.String() + "; stopping")
	s.shutdown(sig)
	if number, ok := sig.(syscall.Signal); ok {
		return 128 + int(number)
	}
	return 1
}

// A stop signal that arrived during the preamble is forwarded once the command starts.
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
