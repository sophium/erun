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
	pid     int
	// handle is the process handle CreateProcess returned via conpty.Spawn. We
	// keep it instead of re-opening the process with os.FindProcess: on
	// locked-down machines an EDR blocks OpenProcess (PROCESS_TERMINATE) and
	// os.FindProcess fails with "OpenProcess: Access is denied.", which used to
	// abort every PTY spawn (env-create, erun open, ...). CreateProcess already
	// handed us a full-rights handle, so no OpenProcess is needed.
	handle  syscall.Handle
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

	pid, handle, err := ptyDevice.Spawn(executable, args, &syscall.ProcAttr{
		Dir: params.Dir,
		Env: env,
	})
	if err != nil {
		_ = ptyDevice.Close()
		return nil, err
	}

	session := &windowsTerminalSession{
		pty:     ptyDevice,
		outPipe: ptyDevice.OutPipe(),
		pid:     pid,
		handle:  syscall.Handle(handle),
	}

	if len(params.InitialInput) > 0 {
		if _, writeErr := ptyDevice.Write(params.InitialInput); writeErr != nil {
			_ = session.Close()
			return nil, writeErr
		}
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
	if s == nil {
		return 0
	}
	return s.pid
}

func (s *windowsTerminalSession) Wait() error {
	if s == nil {
		return nil
	}
	s.wait.Do(func() {
		if s.handle == 0 {
			return
		}
		if _, err := syscall.WaitForSingleObject(s.handle, syscall.INFINITE); err != nil {
			s.waitErr = err
			return
		}
		var code uint32
		if err := syscall.GetExitCodeProcess(s.handle, &code); err == nil && code != 0 {
			s.waitErr = fmt.Errorf("exit status %d", code)
		}
	})
	return s.waitErr
}

func (s *windowsTerminalSession) Close() error {
	if s == nil {
		return nil
	}
	// Kill the whole child tree, not just the shell: an `erun open`'s kubectl
	// exec child otherwise survives as an orphan that holds the exec stream open,
	// leaving a stale dtach client attached in the pod after every close.
	if s.pid > 0 {
		killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(s.pid))
		eruncommon.HideConsoleWindow(killCmd)
		_ = killCmd.Run()
	}
	if s.handle != 0 {
		_ = syscall.CloseHandle(s.handle)
		s.handle = 0
	}
	if s.pty != nil {
		return s.pty.Close()
	}
	return nil
}
