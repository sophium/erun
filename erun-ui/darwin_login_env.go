//go:build darwin

package main

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// loginEnvOnce gates the one-shot login-shell environment import. Wails
// apps launched from Finder or Dock start under launchd with a minimal
// environment (PATH=/usr/bin:/bin:/usr/sbin:/sbin, KUBECONFIG unset,
// AWS_* unset, etc), so subprocess calls like `kubectl config
// get-contexts` either fail to find their binaries or read the wrong
// kubeconfig.
var loginEnvOnce sync.Once

// importLoginShellEnv runs the user's login shell with -ilc to capture
// the env it exports (PATH, KUBECONFIG, AWS_PROFILE, ...) and merges a
// short allowlist of variables into the current process. macOS only;
// Linux desktop GUIs typically inherit the user session's env so the
// import is unnecessary there, and Windows uses its own PATH machinery.
//
// Allowlist rather than blanket merge so we do not stomp on Wails or
// Go runtime variables (LANG, HOME, etc) the caller may have curated.
//
// Idempotent via sync.Once so calling it from both startup and
// individual command sites is safe.
func importLoginShellEnv() {
	loginEnvOnce.Do(func() {
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/sh"
		}
		// `-ilc` runs the shell as both interactive and login, which on
		// zsh sources ~/.zshenv + ~/.zprofile + ~/.zshrc + ~/.zlogin
		// and on bash sources the login-mode profile chain plus
		// ~/.bashrc. Most macOS users export KUBECONFIG and PATH in
		// ~/.zshrc, which `-lc` alone would skip.
		output, err := exec.Command(shell, "-ilc", "/usr/bin/env").Output()
		if err != nil {
			return
		}
		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			idx := strings.IndexByte(line, '=')
			if idx <= 0 {
				continue
			}
			key := line[:idx]
			if !loginEnvKeyAllowed(key) {
				continue
			}
			value := line[idx+1:]
			if value == "" {
				continue
			}
			_ = os.Setenv(key, value)
		}
	})
}

// loginEnvKeyAllowed names environment variables that subprocess calls
// from the desktop process need but launchd-style starts do not
// provide. Keep the allowlist short and predictable; do not blanket-
// import everything from the login shell.
func loginEnvKeyAllowed(key string) bool {
	switch key {
	case "PATH",
		"KUBECONFIG",
		"AWS_PROFILE",
		"AWS_DEFAULT_PROFILE",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"HELM_DRIVER",
		"DOCKER_HOST",
		"DOCKER_CONFIG":
		return true
	}
	return false
}
