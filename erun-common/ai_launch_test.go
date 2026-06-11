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

	t.Run("unset and invalid effort both resolve to ultracode", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{})
		if !strings.Contains(got, `--settings '{"ultracode":true}'`) || strings.Contains(got, "--effort") {
			t.Fatalf("unset effort must default to ultracode via --settings, got %q", got)
		}
		// A bad persisted value must never reach the shell verbatim; it resolves
		// to the default instead of injecting `--effort turbo`.
		got = AISessionLaunchCommand("", effort("turbo"))
		if strings.Contains(got, "turbo") || !strings.Contains(got, `--settings '{"ultracode":true}'`) {
			t.Fatalf("invalid effort must resolve to ultracode, got %q", got)
		}
	})

	// Issue #491 — ultracode is not a `claude --effort` value: it launches
	// through the settings mechanism. The five --effort levels are untouched,
	// and the settings JSON appears in both branches of the cwd-guarded
	// resume, composing after --continue.
	t.Run("ultracode launches via --settings in both guard branches", func(t *testing.T) {
		got := AISessionLaunchCommand("", effort("ultracode"))
		if strings.Count(got, `--settings '{"ultracode":true}'`) != 2 || strings.Contains(got, "--effort") {
			t.Fatalf("ultracode must inject --settings (never --effort) in both branches, got %q", got)
		}
		if !strings.Contains(got, `claude --continue --settings '{"ultracode":true}'`) {
			t.Fatalf("--settings must compose after --continue in the resume branch, got %q", got)
		}
	})

	t.Run("an explicit max still launches via --effort", func(t *testing.T) {
		got := AISessionLaunchCommand("", effort("max"))
		if !strings.Contains(got, "--effort max") || strings.Contains(got, "--settings") {
			t.Fatalf("explicit max must keep --effort max, got %q", got)
		}
	})
}

// TestAISessionLaunchCommandModelAndDebugFlags pins the per-env default model
// and verbose+debug launch flags (issues #482/#477): both compose after
// --effort in both branches of the cwd-guarded resume, and each injects
// independently of the other.
func TestAISessionLaunchCommandModelAndDebugFlags(t *testing.T) {
	t.Run("default model and verbose debug inject into both guard branches", func(t *testing.T) {
		model := "fable"
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{
			Models:       []string{"opus", "fable"},
			DefaultModel: &model,
			VerboseDebug: true,
		})
		if strings.Count(got, "--model fable --verbose --debug") != 2 {
			t.Fatalf("expected --model and --verbose --debug in both guard branches, got %q", got)
		}
		if !strings.Contains(got, `claude --continue --settings '{"ultracode":true}' --model fable --verbose --debug`) {
			t.Fatalf("flags must compose after the effort flags in the resume branch, got %q", got)
		}
	})

	t.Run("verbose debug alone injects without a model", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{VerboseDebug: true})
		if strings.Contains(got, "--model") || strings.Count(got, "--verbose --debug") != 2 {
			t.Fatalf("expected only --verbose --debug in both branches, got %q", got)
		}
	})
}

// TestResolveClaudeDefaultModel covers the per-env default-model resolution
// (issue #482): the model launches only while it is one of the env's available
// models — the explicit models list, or the default available set when the env
// declares none — and only when it is a plain argv token. Everything else
// resolves to "" so no stale, foreign, or unsafe --model reaches the shell.
func TestResolveClaudeDefaultModel(t *testing.T) {
	model := func(v string) *string { return &v }
	cases := []struct {
		name   string
		config EnvironmentClaudeConfig
		want   string
	}{
		{"unset", EnvironmentClaudeConfig{}, ""},
		{"in explicit models", EnvironmentClaudeConfig{Models: []string{"opus", "fable"}, DefaultModel: model("fable")}, "fable"},
		{"not in explicit models", EnvironmentClaudeConfig{Models: []string{"opus"}, DefaultModel: model("fable")}, ""},
		{"in default available set when models unset", EnvironmentClaudeConfig{DefaultModel: model("sonnet")}, "sonnet"},
		{"fable is opt-in, not in the default available set", EnvironmentClaudeConfig{DefaultModel: model("fable")}, ""},
		{"trimmed", EnvironmentClaudeConfig{Models: []string{"opus"}, DefaultModel: model("  opus  ")}, "opus"},
		{"blank", EnvironmentClaudeConfig{DefaultModel: model("  ")}, ""},
		{"unsafe token never reaches the shell", EnvironmentClaudeConfig{Models: []string{"a b; rm"}, DefaultModel: model("a b; rm")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveClaudeDefaultModel(tc.config); got != tc.want {
				t.Fatalf("resolveClaudeDefaultModel(%+v) = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}

// TestResolveClaudeEffort covers the per-env effort resolution: a valid level is
// used; unset, blank, and invalid fall back to ultracode (the default) so the
// launch never carries a bad effort flag.
func TestResolveClaudeEffort(t *testing.T) {
	level := func(v string) *string { return &v }
	cases := []struct {
		name   string
		config EnvironmentClaudeConfig
		want   string
	}{
		{"unset", EnvironmentClaudeConfig{}, "ultracode"},
		{"valid", EnvironmentClaudeConfig{Effort: level("low")}, "low"},
		{"explicit max stays max", EnvironmentClaudeConfig{Effort: level("max")}, "max"},
		{"ultracode is a valid level", EnvironmentClaudeConfig{Effort: level("ultracode")}, "ultracode"},
		{"trimmed", EnvironmentClaudeConfig{Effort: level("  high  ")}, "high"},
		{"blank", EnvironmentClaudeConfig{Effort: level("  ")}, "ultracode"},
		{"invalid", EnvironmentClaudeConfig{Effort: level("turbo")}, "ultracode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveClaudeEffort(tc.config); got != tc.want {
				t.Fatalf("resolveClaudeEffort(%+v) = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}
