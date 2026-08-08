//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type unixTerminalSession struct {
	ptyFile *os.File
	cmd     *exec.Cmd
	wait    sync.Once
	waitErr error
}

func startTerminalSession(params startTerminalSessionParams) (terminalSession, error) {
	cmd := exec.Command(params.Executable, params.Args...)
	cmd.Dir = params.Dir
	cmd.Env = terminalSessionEnv(os.Environ(), params.Env)

	file, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(params.Cols),
		Rows: uint16(params.Rows),
	})
	if err != nil {
		return nil, err
	}

	if len(params.InitialInput) > 0 {
		if _, writeErr := file.Write(params.InitialInput); writeErr != nil {
			_ = file.Close()
			return nil, writeErr
		}
	}

	session := &unixTerminalSession{
		ptyFile: file,
		cmd:     cmd,
	}
	return session, nil
}

func (s *unixTerminalSession) Read(buffer []byte) (int, error) {
	return s.ptyFile.Read(buffer)
}

func (s *unixTerminalSession) Write(buffer []byte) (int, error) {
	return s.ptyFile.Write(buffer)
}

func (s *unixTerminalSession) Resize(cols, rows int) error {
	return pty.Setsize(s.ptyFile, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

func (s *unixTerminalSession) Wait() error {
	if s == nil {
		return nil
	}
	s.wait.Do(func() {
		if s.cmd != nil {
			s.waitErr = s.cmd.Wait()
		}
	})
	return s.waitErr
}

func (s *unixTerminalSession) Pid() int {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Alive probes the process with signal 0. POSIX has no EDR OpenProcess block,
// so a signal-0 send is a reliable liveness check: nil means running, ESRCH
// means gone. Any other error (e.g. EPERM) means the process exists.
func (s *unixTerminalSession) Alive() bool {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return false
	}
	err := s.cmd.Process.Signal(syscall.Signal(0))
	return !errors.Is(err, syscall.ESRCH)
}

func (s *unixTerminalSession) Close() error {
	if s == nil {
		return nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		// Kill the whole process group, not just `erun open`: its kubectl exec
		// child otherwise orphans and holds the exec stream open, leaving a stale
		// dtach client attached in the pod after every close. pty.Start made the
		// child a session leader, so a negative-pid kill reaps the full chain.
		if pid := s.cmd.Process.Pid; pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		_ = s.cmd.Process.Kill()
		_ = s.Wait()
	}
	if s.ptyFile != nil {
		return ignoreAlreadyClosed(s.ptyFile.Close())
	}
	return nil
}
