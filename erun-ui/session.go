package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type terminalSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type startTerminalSessionParams struct {
	Dir        string
	Executable string
	Args       []string
	Env        []string
	Cols       int
	Rows       int
}

func resolveCLIExecutable() string {
	executableName := "erun"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}

	executable, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(executable), executableName)
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling
		}

		devBinary := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "..", "erun-cli", "bin", executableName))
		if info, statErr := os.Stat(devBinary); statErr == nil && !info.IsDir() {
			return devBinary
		}
	}

	if path, err := exec.LookPath(executableName); err == nil {
		return path
	}
	return executableName
}

func buildOpenCommand(cliPath, tenant, environment string) string {
	if runtime.GOOS == "windows" {
		return "& " + powerShellQuote(cliPath) + " open " + powerShellQuote(strings.TrimSpace(tenant)) + " " + powerShellQuote(strings.TrimSpace(environment))
	}
	return shellQuote(cliPath) + " open " + shellQuote(strings.TrimSpace(tenant)) + " " + shellQuote(strings.TrimSpace(environment))
}

func buildOpenArgs(tenant, environment string) []string {
	return []string{"open", strings.TrimSpace(tenant), strings.TrimSpace(environment)}
}

func resolveTerminalStartDir(preferred string) string {
	candidates := []string{strings.TrimSpace(preferred)}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}
	candidates = append(candidates, ".")

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}

	return "."
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
