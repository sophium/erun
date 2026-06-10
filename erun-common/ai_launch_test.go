package eruncommon

import (
	"strings"
	"testing"
)

// TestAISessionLaunchCommand pins the AI tab's pod-side launcher (relocated from
// erun-ui for issue #478): a bare/claude tool is wrapped in the cwd-guarded
// resume with the env effort injected into both branches; any other tool, or
// claude with explicit flags, launches verbatim.
func TestAISessionLaunchCommand(t *testing.T) {
	effort := func(level string) EnvironmentClaudeConfig {
		l := level
		return EnvironmentClaudeConfig{Effort: &l}
	}

	t.Run("default claude wraps in the cwd guard at the env effort", func(t *testing.T) {
		got := AISessionLaunchCommand("", effort("high"))
		if strings.Count(got, "--effort high") != 2 {
			t.Fatalf("expected --effort high in both guard branches, got %q", got)
		}
		if !strings.Contains(got, "claude --continue --effort high") || !strings.Contains(got, "else claude --effort high") {
			t.Fatalf("guard missing the resume/fresh branches: %q", got)
		}
	})

	t.Run("explicit claude tool also uses the guard", func(t *testing.T) {
		if got := AISessionLaunchCommand("claude", effort("low")); !strings.Contains(got, "claude --continue --effort low") {
			t.Fatalf("explicit claude must use the guard, got %q", got)
		}
	})

	t.Run("a non-claude tool launches verbatim", func(t *testing.T) {
		if got := AISessionLaunchCommand("codex", effort("max")); got != "codex" {
			t.Fatalf("codex must launch verbatim, got %q", got)
		}
	})

	t.Run("claude with explicit flags launches verbatim", func(t *testing.T) {
		if got := AISessionLaunchCommand("claude --resume", effort("max")); got != "claude --resume" {
			t.Fatalf("explicit-flag claude must launch verbatim, got %q", got)
		}
	})

	t.Run("unset and invalid effort both resolve to max", func(t *testing.T) {
		if got := AISessionLaunchCommand("", EnvironmentClaudeConfig{}); !strings.Contains(got, "--effort max") {
			t.Fatalf("unset effort must default to max, got %q", got)
		}
		// A bad persisted value must never reach the shell verbatim; it resolves
		// to the default instead of injecting `--effort turbo`.
		got := AISessionLaunchCommand("", effort("turbo"))
		if strings.Contains(got, "turbo") || !strings.Contains(got, "--effort max") {
			t.Fatalf("invalid effort must resolve to max, got %q", got)
		}
	})
}

// TestResolveClaudeEffort covers the per-env effort resolution: a valid level is
// used; unset, blank, and invalid fall back to max so the launch never carries a
// bad --effort flag.
func TestResolveClaudeEffort(t *testing.T) {
	level := func(v string) *string { return &v }
	cases := []struct {
		name   string
		config EnvironmentClaudeConfig
		want   string
	}{
		{"unset", EnvironmentClaudeConfig{}, "max"},
		{"valid", EnvironmentClaudeConfig{Effort: level("low")}, "low"},
		{"trimmed", EnvironmentClaudeConfig{Effort: level("  high  ")}, "high"},
		{"blank", EnvironmentClaudeConfig{Effort: level("  ")}, "max"},
		{"invalid", EnvironmentClaudeConfig{Effort: level("turbo")}, "max"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveClaudeEffort(tc.config); got != tc.want {
				t.Fatalf("resolveClaudeEffort(%+v) = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}
