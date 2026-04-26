//go:build windows

package main

import (
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/ActiveState/termtest/conpty"
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

	env := append(os.Environ(), params.Env...)
	args := append([]string{params.Executable}, params.Args...)

	pid, _, err := ptyDevice.Spawn(params.Executable, args, &syscall.ProcAttr{
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
		_ = s.process.Kill()
		_ = s.Wait()
	}
	if s.pty != nil {
		return s.pty.Close()
	}
	return nil
}
