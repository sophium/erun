//go:build darwin

package main

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Finder/Dock launches get launchd's minimal env (no PATH entries, KUBECONFIG, AWS_*).
var loginEnvOnce sync.Once

// importLoginShellEnv imports an allowlist so curated runtime variables (LANG, HOME) are not stomped.
func importLoginShellEnv() {
	loginEnvOnce.Do(func() {
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/sh"
		}
		// `-ilc` so zsh sources ~/.zshrc, where PATH and KUBECONFIG are usually exported.
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
