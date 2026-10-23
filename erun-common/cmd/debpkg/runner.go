package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runner runs one tool in dir with env added to the tool's own environment.
type runner interface {
	run(dir string, env []string, name string, args ...string) error
	output(dir string, env []string, name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) command(dir string, env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr
	return cmd
}

func (r execRunner) run(dir string, env []string, name string, args ...string) error {
	cmd := r.command(dir, env, name, args...)
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (r execRunner) output(dir string, env []string, name string, args ...string) (string, error) {
	out, err := r.command(dir, env, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}
