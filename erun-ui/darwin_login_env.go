//go:build darwin

package main

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Wails apps launched from Finder or Dock start under launchd with a
// minimal environment (PATH=/usr/bin:/bin:/usr/sbin:/sbin, KUBECONFIG
// and AWS_* unset), so subprocess calls like `kubectl config
// get-contexts` fail to find their binaries or read the wrong kubeconfig.
var loginEnvOnce sync.Once

// importLoginShellEnv uses an allowlist rather than a blanket merge so it
// does not stomp Wails or Go runtime variables (LANG, HOME, ...) the
// caller may have curated. macOS only: Linux desktop GUIs inherit the
// user session env and Windows uses its own PATH machinery.
func importLoginShellEnv() {
	loginEnvOnce.Do(func() {
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/sh"
		}
		// Interactive+login (`-ilc`) so zsh sources ~/.zshrc, where most
		// macOS users export PATH and KUBECONFIG; `-lc` alone would skip it.
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

// loginEnvKeyAllowed keeps the imported set to a short, predictable
// allowlist rather than blanket-importing the whole login-shell environment.
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
