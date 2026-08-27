package eruncommon

import (
	"os/exec"
	"strings"
	"testing"
)

// normalizeClaudeSettingsFlag replaces the exact `--settings '<json>'` flag
// text (computed the same way ai_launch.go's claudeSettingsFlag itself builds
// it, for tenant/environment/sessionID "team"/"dev"/"ai" — what every test in
// this file launches with) with a short placeholder, so the rest of this
// file's assertions can pin flag composition and ordering without repeating
// the full turn-boundary hooks payload inline. A plain string replace, not a
// parse: the payload embeds single-quoted shell commands of its own
// (AISessionStatusReportCommand's printf), so shellQuote's escaping of those
// inner quotes makes naively scanning for the next `'` land inside the
// escape sequence rather than the flag's real end.
//
// The launch command carries the flag text once; the resume line
// (AISessionLaunchLines) carries it a second time already shell-quoted by
// shellQuote(resume), which re-escapes every `'` in the flag text into
// `'"'"'`. Both forms are replaced so this one helper covers both callers.
func normalizeClaudeSettingsFlag(t *testing.T, command string) string {
	t.Helper()
	return normalizeClaudeSettingsFlagFor(t, command, "team", "dev", "ai")
}

func normalizeClaudeSettingsFlagFor(t *testing.T, command, tenant, environment, sessionID string) string {
	t.Helper()
	out := command
	for _, hooks := range []struct {
		raw         string
		placeholder string
	}{
		{claudeSettingsFlag(claudeEffortUltracode, tenant, environment, sessionID), " --settings <HOOKS+ULTRACODE>"},
		{claudeSettingsFlag("low", tenant, environment, sessionID), " --settings <HOOKS>"},
	} {
		out = strings.ReplaceAll(out, hooks.raw, hooks.placeholder)
		out = strings.ReplaceAll(out, strings.ReplaceAll(hooks.raw, "'", `'"'"'`), hooks.placeholder)
	}
	return out
}

// TestAISessionLaunchCommand pins which AI-tab launches wrap in the cwd guard
// (bare or claude) versus launch verbatim (other tools, or claude with explicit
// flags). Remote Control naming is pinned separately by TestAISessionLaunchRemoteControl.
func TestAISessionLaunchCommand(t *testing.T) {
	effort := func(level string) EnvironmentClaudeConfig {
		l := level
		return EnvironmentClaudeConfig{Effort: &l}
	}

	t.Run("default claude wraps in the cwd guard at the env effort", func(t *testing.T) {
		assertDefaultClaudeGuardAtEffort(t, effort("high"))
	})

	t.Run("explicit claude tool also uses the guard", func(t *testing.T) {
		got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("claude", effort("low"), "team", "dev", "ai"))
		if !strings.Contains(got, "claude --continue --settings <HOOKS> --effort low") {
			t.Fatalf("explicit claude must use the guard, got %q", got)
		}
	})

	t.Run("a non-claude tool launches verbatim", func(t *testing.T) {
		if got := AISessionLaunchCommand("codex", effort("max"), "team", "dev", "ai"); got != "codex" {
			t.Fatalf("codex must launch verbatim, got %q", got)
		}
	})

	t.Run("claude with explicit flags launches verbatim", func(t *testing.T) {
		if got := AISessionLaunchCommand("claude --resume", effort("max"), "team", "dev", "ai"); got != "claude --resume" {
			t.Fatalf("explicit-flag claude must launch verbatim, got %q", got)
		}
	})

	t.Run("unset and invalid effort both resolve to ultracode", func(t *testing.T) {
		assertEffortResolvesToUltracode(t, effort)
	})

	// ultracode is not a `claude --effort` value; it launches through the
	// --settings mechanism instead.
	t.Run("ultracode launches via --settings in both guard branches", func(t *testing.T) {
		assertUltracodeInBothBranches(t, effort("ultracode"))
	})

	t.Run("an explicit max still launches via --effort, alongside the status hooks", func(t *testing.T) {
		got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", effort("max"), "team", "dev", "ai"))
		if !strings.Contains(got, "--settings <HOOKS> --effort max") || strings.Contains(got, "<HOOKS+ULTRACODE>") {
			t.Fatalf("explicit max must keep --effort max alongside hooks-only settings, got %q", got)
		}
	})
}

func assertDefaultClaudeGuardAtEffort(t *testing.T, config EnvironmentClaudeConfig) {
	t.Helper()
	got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", config, "team", "dev", "ai"))
	if strings.Count(got, "--settings <HOOKS> --effort high --model opus") != 2 {
		t.Fatalf("expected --settings <HOOKS> --effort high --model opus in both guard branches, got %q", got)
	}
	if strings.Count(got, "CLAUDE_CODE_SUBAGENT_MODEL=opus claude") != 2 {
		t.Fatalf("expected the opus subagent-model prefix in both guard branches, got %q", got)
	}
	if !strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL=opus claude --continue --settings <HOOKS> --effort high --model opus") ||
		!strings.Contains(got, "else CLAUDE_CODE_SUBAGENT_MODEL=opus claude --settings <HOOKS> --effort high --model opus") {
		t.Fatalf("guard missing the resume/fresh branches: %q", got)
	}
}

func assertEffortResolvesToUltracode(t *testing.T, effort func(string) EnvironmentClaudeConfig) {
	t.Helper()
	got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", EnvironmentClaudeConfig{}, "team", "dev", "ai"))
	if !strings.Contains(got, "--settings <HOOKS+ULTRACODE>") || strings.Contains(got, "--effort") {
		t.Fatalf("unset effort must default to ultracode via --settings, got %q", got)
	}
	// A bad persisted value must never reach the shell verbatim; it resolves
	// to the default instead of injecting `--effort turbo`.
	got = normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", effort("turbo"), "team", "dev", "ai"))
	if strings.Contains(got, "turbo") || !strings.Contains(got, "--settings <HOOKS+ULTRACODE>") {
		t.Fatalf("invalid effort must resolve to ultracode, got %q", got)
	}
}

func assertUltracodeInBothBranches(t *testing.T, config EnvironmentClaudeConfig) {
	t.Helper()
	got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", config, "team", "dev", "ai"))
	if strings.Count(got, "--settings <HOOKS+ULTRACODE>") != 2 || strings.Contains(got, "--effort") {
		t.Fatalf("ultracode must inject --settings (never --effort) in both branches, got %q", got)
	}
	if !strings.Contains(got, "claude --continue --settings <HOOKS+ULTRACODE>") {
		t.Fatalf("--settings must compose after --continue in the resume branch, got %q", got)
	}
}

// TestAISessionLaunchCommandModelAndDebugFlags pins that the per-env default
// model and verbose+debug launch flags each inject independently of the other.
func TestAISessionLaunchCommandModelAndDebugFlags(t *testing.T) {
	t.Run("default model and verbose debug inject into both guard branches", func(t *testing.T) {
		model := "fable"
		got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", EnvironmentClaudeConfig{
			Models:       []string{"opus", "fable"},
			DefaultModel: &model,
			VerboseDebug: true,
		}, "team", "dev", "ai"))
		if strings.Count(got, "--model fable --verbose --debug") != 2 {
			t.Fatalf("expected --model and --verbose --debug in both guard branches, got %q", got)
		}
		if !strings.Contains(got, "claude --continue --settings <HOOKS+ULTRACODE> --model fable --verbose --debug") {
			t.Fatalf("flags must compose after the effort flags in the resume branch, got %q", got)
		}
	})

	t.Run("verbose debug composes with the resolved default model", func(t *testing.T) {
		got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", EnvironmentClaudeConfig{VerboseDebug: true}, "team", "dev", "ai"))
		if strings.Count(got, "--model opus --verbose --debug") != 2 {
			t.Fatalf("expected --model opus --verbose --debug in both branches, got %q", got)
		}
	})
}

// TestAISessionLaunchSubagentModelPrefix pins that the subagent model mirrors
// the launch model (CLAUDE_CODE_SUBAGENT_MODEL is set from --model), and is
// absent when no model resolves or the tool is not claude.
func TestAISessionLaunchSubagentModelPrefix(t *testing.T) {
	model := func(v string) *string { return &v }

	t.Run("prefix mirrors the resolved model on both guard branches and composes with ultracode", func(t *testing.T) {
		got := normalizeClaudeSettingsFlag(t, AISessionLaunchCommand("", EnvironmentClaudeConfig{
			Models:       []string{"fable"},
			DefaultModel: model("fable"),
		}, "team", "dev", "ai"))
		if strings.Count(got, "CLAUDE_CODE_SUBAGENT_MODEL=fable claude") != 2 {
			t.Fatalf("expected the subagent-model prefix on both guard branches, got %q", got)
		}
		if !strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL=fable claude --continue --settings <HOOKS+ULTRACODE> --model fable") {
			t.Fatalf("prefix must precede claude and compose with --settings/--model, got %q", got)
		}
	})

	t.Run("no prefix when no available model is a safe token", func(t *testing.T) {
		// A launch carries no model only when every available model is
		// unusable; nothing unusable is ever started.
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{Models: []string{"a b; rm"}}, "team", "dev", "ai")
		if strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL") || strings.Contains(got, "--model") {
			t.Fatalf("expected no subagent-model prefix or --model without a safe model, got %q", got)
		}
	})

	t.Run("no prefix for a non-claude tool", func(t *testing.T) {
		if got := AISessionLaunchCommand("codex", EnvironmentClaudeConfig{Models: []string{"fable"}, DefaultModel: model("fable")}, "team", "dev", "ai"); strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL") {
			t.Fatalf("non-claude tool must launch verbatim with no prefix, got %q", got)
		}
	})

	t.Run("resume line carries the prefix when a model resolves", func(t *testing.T) {
		script := normalizeClaudeSettingsFlag(t, strings.Join(AISessionLaunchLines("", EnvironmentClaudeConfig{
			Models:       []string{"fable"},
			DefaultModel: model("fable"),
		}, "team", "dev", "ai"), "\n"))
		if !strings.Contains(script, "resume with") || !strings.Contains(script, "CLAUDE_CODE_SUBAGENT_MODEL=fable claude --continue") {
			t.Fatalf("resume command must carry the subagent-model prefix:\n%s", script)
		}
	})

	t.Run("resume line has no prefix for a non-claude tool", func(t *testing.T) {
		script := strings.Join(AISessionLaunchLines("codex", EnvironmentClaudeConfig{Models: []string{"fable"}, DefaultModel: model("fable")}, "team", "dev", "ai"), "\n")
		if strings.Contains(script, "CLAUDE_CODE_SUBAGENT_MODEL") {
			t.Fatalf("non-claude resume must not carry the prefix:\n%s", script)
		}
	})
}

// TestAISessionLaunchRemoteControl pins the default Remote Control launch: the
// managed claude session is named <tenant>/<env> so the operator can drive it
// from the Claude iOS app. It is gated off for the Bedrock/Mantle gateways —
// the claude.ai account relay it pairs through cannot authenticate those — and
// for a non-claude tool, and falls back to the unnamed flag (Claude Code then
// names the session from the pod hostname) when the tenant/env is not safe to
// inline.
func TestAISessionLaunchRemoteControl(t *testing.T) {
	yes := func() *bool { b := true; return &b }

	t.Run("named <tenant>/<env> by default in both guard branches", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, "team", "dev", "ai")
		if strings.Count(got, "--remote-control team/dev") != 2 {
			t.Fatalf("expected --remote-control team/dev in both guard branches, got %q", got)
		}
	})

	t.Run("gated off when the env uses the Bedrock gateway", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{UseBedrock: yes()}, "team", "dev", "ai")
		if strings.Contains(got, "--remote-control") {
			t.Fatalf("Bedrock gateway auth must not enable remote control, got %q", got)
		}
	})

	t.Run("gated off when the env uses the Mantle gateway", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{UseMantle: yes()}, "team", "dev", "ai")
		if strings.Contains(got, "--remote-control") {
			t.Fatalf("Mantle gateway auth must not enable remote control, got %q", got)
		}
	})

	t.Run("an unsafe tenant/env falls back to the unnamed flag", func(t *testing.T) {
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, "a b; rm", "dev", "ai")
		if strings.Count(got, "--remote-control") != 2 || strings.Contains(got, "--remote-control ") {
			t.Fatalf("unsafe tenant must yield the unnamed flag in both branches, got %q", got)
		}
	})

	t.Run("a leading-dash tenant falls back to the unnamed flag", func(t *testing.T) {
		// Shell-safe, but Claude Code's own arg parser would read a
		// "-"-leading name as an option flag, so it is treated as unsafe.
		got := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, "-rm", "dev", "ai")
		if strings.Count(got, "--remote-control") != 2 || strings.Contains(got, "--remote-control ") {
			t.Fatalf("leading-dash tenant must yield the unnamed flag, got %q", got)
		}
	})

	t.Run("no remote control for a non-claude tool", func(t *testing.T) {
		if got := AISessionLaunchCommand("codex", EnvironmentClaudeConfig{}, "team", "dev", "ai"); strings.Contains(got, "--remote-control") {
			t.Fatalf("non-claude tool must launch verbatim without remote control, got %q", got)
		}
	})

	t.Run("resume line carries the named flag", func(t *testing.T) {
		script := strings.Join(AISessionLaunchLines("", EnvironmentClaudeConfig{}, "team", "dev", "ai"), "\n")
		if !strings.Contains(script, "--remote-control team/dev") {
			t.Fatalf("resume line must carry the named remote-control flag:\n%s", script)
		}
	})
}

// TestResolveClaudeLaunchModel covers launch-model resolution: a chosen default
// wins when available, else the first available model — never the agent's own
// default. fable is opt-in (the env must both list and select it), and an
// all-unusable set resolves to none.
func TestResolveClaudeLaunchModel(t *testing.T) {
	model := func(v string) *string { return &v }
	cases := []struct {
		name   string
		config EnvironmentClaudeConfig
		want   string
	}{
		{"unset falls back to the first default-available model", EnvironmentClaudeConfig{}, "opus"},
		{"explicit model in explicit models", EnvironmentClaudeConfig{Models: []string{"opus", "fable"}, DefaultModel: model("fable")}, "fable"},
		{"explicit model not in explicit models falls back to first available", EnvironmentClaudeConfig{Models: []string{"opus"}, DefaultModel: model("fable")}, "opus"},
		{"explicit model in default available set when models unset", EnvironmentClaudeConfig{DefaultModel: model("sonnet")}, "sonnet"},
		{"fable is opt-in: not auto-selected from the default available set", EnvironmentClaudeConfig{DefaultModel: model("fable")}, "opus"},
		{"first-available honours the env's narrowed set", EnvironmentClaudeConfig{Models: []string{"sonnet", "haiku"}}, "sonnet"},
		{"trimmed explicit model", EnvironmentClaudeConfig{Models: []string{"opus"}, DefaultModel: model("  opus  ")}, "opus"},
		{"blank explicit model falls back to first available", EnvironmentClaudeConfig{DefaultModel: model("  ")}, "opus"},
		{"unsafe explicit model falls back to first available", EnvironmentClaudeConfig{Models: []string{"opus"}, DefaultModel: model("a b; rm")}, "opus"},
		{"no safe token in the available set resolves to empty", EnvironmentClaudeConfig{Models: []string{"a b; rm"}, DefaultModel: model("a b; rm")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveClaudeLaunchModel(tc.config); got != tc.want {
				t.Fatalf("resolveClaudeLaunchModel(%+v) = %q, want %q", tc.config, got, tc.want)
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

// TestAISessionLaunchLines pins the AI session's exit wrapper: the tool's exit
// must never silently fall through to the trailing shell. A real claude exit
// can't be staged in the headless Playwright harness, so these script-content
// assertions own the contract; the launcher's composition into the dtach script
// is locked by the open --ai dry-run goldens.
func TestAISessionLaunchLines(t *testing.T) {
	t.Run("claude guard wraps with status capture, OOM hint, and quoted resume", func(t *testing.T) {
		script := normalizeClaudeSettingsFlag(t, strings.Join(AISessionLaunchLines("", EnvironmentClaudeConfig{}, "team", "dev", "ai"), "\n"))
		for _, want := range []string{
			"ai_status=0",
			"fi || ai_status=$?",
			`[ "$ai_status" = 137 ]`,
			"likely out of memory; consider raising Memory in the environment Runtime settings",
			"Claude exited (exit %s)",
			"Claude session ended",
			"resume with: %s",
			// The exit outcome is recorded before the human-facing banner: a
			// client reading the status file must never see the banner without
			// the file it renders being already true.
			`"/tmp/erun-sessions-status/team-dev-ai.exit.json"`,
		} {
			if !strings.Contains(script, want) {
				t.Fatalf("wrapper missing %q:\n%s", want, script)
			}
		}
		if strings.Index(script, "ai.exit.json") > strings.Index(script, "resume with") {
			t.Fatalf("exit outcome must be recorded before the resume banner is printed:\n%s", script)
		}
		// The resume is shell-quoted because it embeds single quotes itself
		// (the --settings placeholder still carries its original brackets).
		if !strings.Contains(script, `'CLAUDE_CODE_SUBAGENT_MODEL=opus claude --continue --settings <HOOKS+ULTRACODE> --model opus --remote-control team/dev'`) {
			t.Fatalf("resume command not safely shell-quoted:\n%s", script)
		}
	})

	t.Run("the wrapper executes: 137 yields the OOM marker, the exit report, and the intact resume", func(t *testing.T) {
		// The exit-report command writes to the real RemoteAppSessionStatusDir
		// constant; redirect it into a temp dir by rewriting that one path in
		// the generated script rather than touching the process's real /tmp.
		statusDir := t.TempDir()
		lines := AISessionLaunchLines("", EnvironmentClaudeConfig{}, "team", "dev", "ai")
		// Swap the launch (line index 1 by construction) for a bare 137 exit to
		// simulate the OOM kill; running the rest through a real sh verifies the
		// printf escapes, the exit-report write, and the shell-quoted resume end
		// to end.
		lines[1] = "(exit 137) || ai_status=$?"
		for i, line := range lines {
			lines[i] = strings.ReplaceAll(line, RemoteAppSessionStatusDir, statusDir)
		}
		out, err := exec.Command("sh", "-c", strings.Join(lines, "\n")).CombinedOutput()
		if err != nil {
			t.Fatalf("wrapper script failed: %v\n%s", err, out)
		}
		text := string(out)
		if !strings.Contains(text, "Claude was killed (exit 137)") {
			t.Fatalf("expected the OOM marker, got:\n%s", text)
		}
		exit, ok := readAISessionExitReport(statusDir, "team", "dev", "ai")
		if !ok || exit.Outcome != AISessionOutcomeOOMKilled || exit.ExitCode != 137 {
			t.Fatalf("expected an oom-killed/137 exit report, got %+v (ok=%v)", exit, ok)
		}
		if !strings.Contains(text, "resume with: CLAUDE_CODE_SUBAGENT_MODEL=opus claude --continue --settings '") {
			t.Fatalf("expected the resume command with its settings flag intact, got:\n%s", text)
		}
		if !strings.Contains(text, "--model opus --remote-control team/dev") {
			t.Fatalf("expected the resume command's trailing flags intact, got:\n%s", text)
		}
	})

	t.Run("a verbatim tool keeps its own resume, a tool-neutral label, and still records its exit", func(t *testing.T) {
		script := strings.Join(AISessionLaunchLines("codex", EnvironmentClaudeConfig{}, "team", "dev", "ai"), "\n")
		if !strings.Contains(script, "codex || ai_status=$?") {
			t.Fatalf("verbatim tool must run unmodified ahead of the wrapper:\n%s", script)
		}
		if !strings.Contains(script, "The AI tool exited (exit %s)") {
			t.Fatalf("verbatim tool label wrong:\n%s", script)
		}
		if !strings.Contains(script, "'codex'") {
			t.Fatalf("verbatim tool resume should be the tool itself:\n%s", script)
		}
		if !strings.Contains(script, `"/tmp/erun-sessions-status/team-dev-ai.exit.json"`) {
			t.Fatalf("a non-claude tool must still record its exit outcome (degrade to unknown state, not unknown outcome):\n%s", script)
		}
	})
}
