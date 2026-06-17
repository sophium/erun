package eruncommon

import (
	"os/exec"
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
		assertDefaultClaudeGuardAtEffort(t, effort("high"))
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
		assertEffortResolvesToUltracode(t, effort)
	})

	// Issue #491 — ultracode is not a `claude --effort` value: it launches
	// through the settings mechanism. The five --effort levels are untouched,
	// and the settings JSON appears in both branches of the cwd-guarded
	// resume, composing after --continue.
	t.Run("ultracode launches via --settings in both guard branches", func(t *testing.T) {
		assertUltracodeInBothBranches(t, effort("ultracode"))
	})

	t.Run("an explicit max still launches via --effort", func(t *testing.T) {
		got := AISessionLaunchCommand("", effort("max"))
		if !strings.Contains(got, "--effort max") || strings.Contains(got, "--settings") {
			t.Fatalf("explicit max must keep --effort max, got %q", got)
		}
	})
}

// assertDefaultClaudeGuardAtEffort pins that a default claude launch wraps in
// the cwd guard with the env's effort level injected into both the resume and
// the fresh branch.
func assertDefaultClaudeGuardAtEffort(t *testing.T, config EnvironmentClaudeConfig) {
	t.Helper()
	got := AISessionLaunchCommand("", config)
	if strings.Count(got, "--effort high") != 2 {
		t.Fatalf("expected --effort high in both guard branches, got %q", got)
	}
	if !strings.Contains(got, "claude --continue --effort high") || !strings.Contains(got, "else claude --effort high") {
		t.Fatalf("guard missing the resume/fresh branches: %q", got)
	}
}

// assertEffortResolvesToUltracode pins that both an unset and an invalid effort
// fall back to the ultracode --settings launch, never a bad --effort flag.
func assertEffortResolvesToUltracode(t *testing.T, effort func(string) EnvironmentClaudeConfig) {
	t.Helper()
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
}

// assertUltracodeInBothBranches pins that ultracode injects the --settings JSON
// (never --effort) into both guard branches and composes after --continue.
func assertUltracodeInBothBranches(t *testing.T, config EnvironmentClaudeConfig) {
	t.Helper()
	got := AISessionLaunchCommand("", config)
	if strings.Count(got, `--settings '{"ultracode":true}'`) != 2 || strings.Contains(got, "--effort") {
		t.Fatalf("ultracode must inject --settings (never --effort) in both branches, got %q", got)
	}
	if !strings.Contains(got, `claude --continue --settings '{"ultracode":true}'`) {
		t.Fatalf("--settings must compose after --continue in the resume branch, got %q", got)
	}
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

// TestAISessionLaunchSubagentModelPrefix pins the CLAUDE_CODE_SUBAGENT_MODEL
// mirror (issue #528): whatever model is passed to --model is also exported as
// the subagent model via a command-string env prefix on both guard branches
// and the resume line, and nothing is exported when no model resolves or the
// tool is not claude.
func TestAISessionLaunchSubagentModelPrefix(t *testing.T) {
	model := func(v string) *string { return &v }

	t.Run("prefix mirrors the resolved model on both guard branches and composes with ultracode", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{
			Models:       []string{"fable"},
			DefaultModel: model("fable"),
		})
		if strings.Count(got, "CLAUDE_CODE_SUBAGENT_MODEL=fable claude") != 2 {
			t.Fatalf("expected the subagent-model prefix on both guard branches, got %q", got)
		}
		// The prefix must sit before claude and leave the single-quoted
		// ultracode --settings JSON intact.
		if !strings.Contains(got, `CLAUDE_CODE_SUBAGENT_MODEL=fable claude --continue --settings '{"ultracode":true}' --model fable`) {
			t.Fatalf("prefix must precede claude and compose with --settings/--model, got %q", got)
		}
	})

	t.Run("no prefix when no model resolves", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{VerboseDebug: true})
		if strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL") {
			t.Fatalf("expected no subagent-model prefix without a resolved model, got %q", got)
		}
	})

	t.Run("no prefix for a non-claude tool", func(t *testing.T) {
		if got := AISessionLaunchCommand("codex", EnvironmentClaudeConfig{Models: []string{"fable"}, DefaultModel: model("fable")}); strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL") {
			t.Fatalf("non-claude tool must launch verbatim with no prefix, got %q", got)
		}
	})

	t.Run("resume line carries the prefix when a model resolves", func(t *testing.T) {
		script := strings.Join(AISessionLaunchLines("", EnvironmentClaudeConfig{
			Models:       []string{"fable"},
			DefaultModel: model("fable"),
		}), "\n")
		if !strings.Contains(script, "resume with") || !strings.Contains(script, "CLAUDE_CODE_SUBAGENT_MODEL=fable claude --continue") {
			t.Fatalf("resume command must carry the subagent-model prefix:\n%s", script)
		}
	})

	t.Run("resume line has no prefix for a non-claude tool", func(t *testing.T) {
		script := strings.Join(AISessionLaunchLines("codex", EnvironmentClaudeConfig{Models: []string{"fable"}, DefaultModel: model("fable")}), "\n")
		if strings.Contains(script, "CLAUDE_CODE_SUBAGENT_MODEL") {
			t.Fatalf("non-claude resume must not carry the prefix:\n%s", script)
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

// TestAISessionLaunchLines pins the AI session's exit wrapper (issue #464):
// the tool's exit must never silently fall through to the trailing shell —
// the wrapper captures the exit status, names it (137 with the OOM hint),
// and prints the exact resume command. A real claude exit can't be staged in
// the headless Playwright harness, so these script-content assertions own
// the contract (the #331 pattern); the launcher composition into the dtach
// script is locked by the open --ai dry-run goldens.
func TestAISessionLaunchLines(t *testing.T) {
	t.Run("claude guard wraps with status capture, OOM hint, and quoted resume", func(t *testing.T) {
		script := strings.Join(AISessionLaunchLines("", EnvironmentClaudeConfig{}), "\n")
		for _, want := range []string{
			"ai_status=0",
			"fi || ai_status=$?",
			`[ "$ai_status" = 137 ]`,
			"likely out of memory; consider raising Memory in the environment Runtime settings",
			"Claude exited (exit %s)",
			"Claude session ended",
			"resume with: %s",
		} {
			if !strings.Contains(script, want) {
				t.Fatalf("wrapper missing %q:\n%s", want, script)
			}
		}
		// The resume command goes through shellQuote as a printf argument —
		// it carries single quotes itself (the ultracode --settings JSON),
		// which would break the printf format if inlined.
		if !strings.Contains(script, `'claude --continue --settings '"'"'{"ultracode":true}'"'"''`) {
			t.Fatalf("resume command not safely shell-quoted:\n%s", script)
		}
	})

	t.Run("the wrapper executes: 137 yields the OOM marker and the intact resume", func(t *testing.T) {
		lines := AISessionLaunchLines("", EnvironmentClaudeConfig{})
		// Swap the launch (line index 1 by construction) for a bare 137
		// exit, simulating the OOM kill; everything after is the wrapper
		// under test, run through a real sh so the printf escapes and the
		// shell-quoted resume are verified end to end.
		lines[1] = "(exit 137) || ai_status=$?"
		out, err := exec.Command("sh", "-c", strings.Join(lines, "\n")).CombinedOutput()
		if err != nil {
			t.Fatalf("wrapper script failed: %v\n%s", err, out)
		}
		text := string(out)
		if !strings.Contains(text, "Claude was killed (exit 137)") {
			t.Fatalf("expected the OOM marker, got:\n%s", text)
		}
		if !strings.Contains(text, `resume with: claude --continue --settings '{"ultracode":true}'`) {
			t.Fatalf("expected the resume command with its quotes intact, got:\n%s", text)
		}
	})

	t.Run("a verbatim tool keeps its own resume and a tool-neutral label", func(t *testing.T) {
		script := strings.Join(AISessionLaunchLines("codex", EnvironmentClaudeConfig{}), "\n")
		if !strings.Contains(script, "codex || ai_status=$?") {
			t.Fatalf("verbatim tool must run unmodified ahead of the wrapper:\n%s", script)
		}
		if !strings.Contains(script, "The AI tool exited (exit %s)") {
			t.Fatalf("verbatim tool label wrong:\n%s", script)
		}
		if !strings.Contains(script, "'codex'") {
			t.Fatalf("verbatim tool resume should be the tool itself:\n%s", script)
		}
	})
}
