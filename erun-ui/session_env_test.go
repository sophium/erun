package main

import (
	"slices"
	"strings"
	"testing"
)

// A terminal the desktop opens is a fresh top-level session, so what the desktop
// itself was launched with must not decide how the child renders or how a Claude
// Code inside it identifies itself.

func envValue(t *testing.T, env []string, name string) (string, bool) {
	t.Helper()
	value := ""
	found := false
	for _, entry := range env {
		key, entryValue, ok := strings.Cut(entry, "=")
		if ok && key == name {
			value = entryValue
			found = true
		}
	}
	return value, found
}

func TestTerminalSessionEnvStripsColorSuppression(t *testing.T) {
	env := terminalSessionEnv([]string{"NO_COLOR=1", "FORCE_COLOR=0", "PATH=/usr/bin"}, nil)

	for _, name := range []string{"NO_COLOR", "FORCE_COLOR"} {
		if value, found := envValue(t, env, name); found {
			t.Fatalf("%s survived into the child env as %q", name, value)
		}
	}
	if value, _ := envValue(t, env, "PATH"); value != "/usr/bin" {
		t.Fatalf("PATH = %q, want the inherited value", value)
	}
	if value, _ := envValue(t, env, "TERM"); value != "xterm-256color" {
		t.Fatalf("TERM = %q", value)
	}
	if value, _ := envValue(t, env, "COLORTERM"); value != "truecolor" {
		t.Fatalf("COLORTERM = %q", value)
	}
}

func TestTerminalSessionEnvStripsForeignClaudeSessionMarkers(t *testing.T) {
	inherited := []string{
		"CLAUDECODE=1",
		"CLAUDE_PID=4321",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		"HOME=/home/dev",
	}

	env := terminalSessionEnv(inherited, nil)

	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "CLAUDE") {
			t.Fatalf("foreign Claude Code marker %q survived into the child env", entry)
		}
	}
	if value, _ := envValue(t, env, "HOME"); value != "/home/dev" {
		t.Fatalf("HOME = %q, want the inherited value", value)
	}
}

func TestTerminalSessionEnvLetsTheCallerOverrideAStrippedName(t *testing.T) {
	// The orchestrator sets CLAUDE_CODE_SUBAGENT_MODEL for the session it is
	// launching, which is the desktop's own decision rather than an inherited
	// marker, so it must survive the scrub.
	requested := []string{"CLAUDE_CODE_SUBAGENT_MODEL=opus", "NO_COLOR=1"}

	env := terminalSessionEnv([]string{"CLAUDE_CODE_SUBAGENT_MODEL=haiku"}, requested)

	if value, found := envValue(t, env, "CLAUDE_CODE_SUBAGENT_MODEL"); !found || value != "opus" {
		t.Fatalf("CLAUDE_CODE_SUBAGENT_MODEL = %q (found %v), want the caller's value", value, found)
	}
	if value, found := envValue(t, env, "NO_COLOR"); !found || value != "1" {
		t.Fatalf("NO_COLOR = %q (found %v), want the caller's explicit value", value, found)
	}
	// The terminal capabilities are appended last so they are what the child ends
	// up with, but a caller that names them explicitly is not the case here.
	if index := slices.Index(env, "TERM=xterm-256color"); index != len(env)-2 {
		t.Fatalf("TERM sits at index %d of %d, want the second-to-last entry", index, len(env))
	}
}
