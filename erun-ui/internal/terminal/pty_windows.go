//go:build windows

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/ActiveState/termtest/conpty"
	"github.com/sophium/erun/erun-common/base/core"
)

type windowsTerminalSession struct {
	pty     *conpty.ConPty
	outPipe *os.File
	pid     int
	// handle comes from CreateProcess; os.FindProcess needs OpenProcess, which EDR agents deny.
	handle  syscall.Handle
	wait    sync.Once
	waitErr error
}

func StartPTY(params StartParams) (PTY, error) {
	ptyDevice, err := conpty.New(int16(params.Cols), int16(params.Rows))
	if err != nil {
		return nil, err
	}

	env := terminalSessionEnv(os.Environ(), params.Env)

	// ConPTY resolves a bare executable against attr.Dir, not PATH.
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

// Alive uses GetExitCodeProcess on the held handle; a failed query assumes alive.
func (s *windowsTerminalSession) Alive() bool {
	if s == nil || s.handle == 0 {
		return false
	}
	const stillActive = 259
	var code uint32
	if err := syscall.GetExitCodeProcess(s.handle, &code); err != nil {
		return true
	}
	return code == stillActive
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
	// Kill the whole tree so the kubectl exec child does not orphan. Probe liveness
	// before closing the handle Alive reads.
	killErr := killProcessTree(s.pid, func() error {
		killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(s.pid))
		core.HideConsoleWindow(killCmd)
		return killCmd.Run()
	}, s.Alive)
	if s.handle != 0 {
		_ = syscall.CloseHandle(s.handle)
		s.handle = 0
	}
	if s.pty != nil {
		ptyErr := ignoreAlreadyClosed(s.pty.Close())
		// A survivor is what the caller must act on; a pty release error is secondary.
		if killErr == nil {
			return ptyErr
		}
	}
	return killErr
}
