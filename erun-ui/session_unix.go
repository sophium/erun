//go:build !windows

package main

import (
	"os"
	"os/exec"
	"sync"

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
	cmd.Env = append(os.Environ(), append(params.Env, "TERM=xterm-256color")...)

	file, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(params.Cols),
		Rows: uint16(params.Rows),
	})
	if err != nil {
		return nil, err
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

func (s *unixTerminalSession) Close() error {
	if s == nil {
		return nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.Wait()
	}
	if s.ptyFile != nil {
		return s.ptyFile.Close()
	}
	return nil
}
