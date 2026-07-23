//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/ActiveState/termtest/conpty"
	eruncommon "github.com/sophium/erun/erun-common"
)

type windowsTerminalSession struct {
	pty     *conpty.ConPty
	outPipe *os.File
	process *os.Process
	wait    sync.Once
	waitErr error
}

func startTerminalSession(params startTerminalSessionParams) (terminalSession, error) {
	ptyDevice, err := conpty.New(int16(params.Cols), int16(params.Rows))
	if err != nil {
		return nil, err
	}

	env := append(os.Environ(), append(params.Env, "TERM=xterm-256color", "COLORTERM=truecolor")...)

	// ConPTY's Spawn resolves a non-absolute executable relative to attr.Dir
	// rather than searching PATH, so a bare name like "powershell.exe" would be
	// looked for in the session's start dir and fail with "the system cannot find
	// the file specified". Resolve it on PATH to an absolute path first.
	executable := params.Executable
	if !filepath.IsAbs(executable) {
		if resolved, lookErr := exec.LookPath(executable); lookErr == nil {
			executable = resolved
		}
	}
	args := append([]string{executable}, params.Args...)

	pid, _, err := ptyDevice.Spawn(executable, args, &syscall.ProcAttr{
		Dir: params.Dir,
		Env: env,
	})
	if err != nil {
		_ = ptyDevice.Close()
		return nil, err
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		_ = ptyDevice.Close()
		return nil, err
	}

	if len(params.InitialInput) > 0 {
		if _, writeErr := ptyDevice.Write(params.InitialInput); writeErr != nil {
			_ = process.Kill()
			_ = ptyDevice.Close()
			return nil, writeErr
		}
	}

	session := &windowsTerminalSession{
		pty:     ptyDevice,
		outPipe: ptyDevice.OutPipe(),
		process: process,
	}
	return session, nil
}

func (s *windowsTerminalSession) Read(buffer []byte) (int, error) {
	return s.outPipe.Read(buffer)
}

func (s *windowsTerminalSession) Write(buffer []byte) (int, error) {
	written, err := s.pty.Write(buffer)
	return int(written), err
}

func (s *windowsTerminalSession) Resize(cols, rows int) error {
	return s.pty.Resize(uint16(cols), uint16(rows))
}

func (s *windowsTerminalSession) Pid() int {
	if s == nil || s.process == nil {
		return 0
	}
	return s.process.Pid
}

func (s *windowsTerminalSession) Wait() error {
	if s == nil {
		return nil
	}
	s.wait.Do(func() {
		if s.process != nil {
			state, err := s.process.Wait()
			if err != nil {
				s.waitErr = err
			} else if state != nil && state.ExitCode() != 0 {
				s.waitErr = fmt.Errorf("exit status %d", state.ExitCode())
			}
		}
	})
	return s.waitErr
}

func (s *windowsTerminalSession) Close() error {
	if s == nil {
		return nil
	}
	if s.process != nil {
		// Kill the whole child tree, not just `erun open`: its kubectl exec
		// child otherwise survives as an orphan that holds the exec stream
		// open, leaving a stale dtach client attached in the pod after every
		// close.
		if s.process.Pid > 0 {
			killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(s.process.Pid))
			eruncommon.HideConsoleWindow(killCmd)
			_ = killCmd.Run()
		}
		_ = s.process.Kill()
		_ = s.Wait()
	}
	if s.pty != nil {
		return s.pty.Close()
	}
	return nil
}
