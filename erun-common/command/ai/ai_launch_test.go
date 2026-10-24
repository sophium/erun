package ai

import (
	"os/exec"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
)

func TestAISessionLaunchCommand(t *testing.T) {
	effort := func(level string) configpkg.EnvironmentClaudeConfig {
		l := level
		return configpkg.EnvironmentClaudeConfig{Effort: &l}
	}

	t.Run("a non-claude tool launches verbatim", func(t *testing.T) {
		if got := open.AISessionLaunchCommand("codex", effort("max"), nil, "team", "dev"); got != "codex" {
			t.Fatalf("codex must launch verbatim, got %q", got)
		}
	})

	t.Run("claude with explicit flags launches verbatim", func(t *testing.T) {
		if got := open.AISessionLaunchCommand("claude --resume", effort("max"), nil, "team", "dev"); got != "claude --resume" {
			t.Fatalf("explicit-flag claude must launch verbatim, got %q", got)
		}
	})
}

func TestAISessionLaunchSubagentModelPrefix(t *testing.T) {
	model := func(v string) *string { return &v }

	t.Run("no prefix when no available model is a safe token", func(t *testing.T) {
		got := open.AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{Models: []string{"a b; rm"}}, nil, "team", "dev")
		if strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL") || strings.Contains(got, "--model") {
			t.Fatalf("expected no subagent-model prefix or --model without a safe model, got %q", got)
		}
	})

	t.Run("no prefix for a non-claude tool", func(t *testing.T) {
		if got := open.AISessionLaunchCommand("codex", configpkg.EnvironmentClaudeConfig{Models: []string{"fable"}, DefaultModel: model("fable")}, nil, "team", "dev"); strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL") {
			t.Fatalf("non-claude tool must launch verbatim with no prefix, got %q", got)
		}
	})

	t.Run("resume line has no prefix for a non-claude tool", func(t *testing.T) {
		script := strings.Join(open.AISessionLaunchLines("codex", configpkg.EnvironmentClaudeConfig{Models: []string{"fable"}, DefaultModel: model("fable")}, nil, "team", "dev"), "\n")
		if strings.Contains(script, "CLAUDE_CODE_SUBAGENT_MODEL") {
			t.Fatalf("non-claude resume must not carry the prefix:\n%s", script)
		}
	})
}

// An opted-out env runs on its own sign-in, so the catalog's context window does not apply.
func TestAISessionLaunchContextFollowsTheOptOut(t *testing.T) {
	model := func(v string) *string { return &v }
	optOut := false
	catalog := &configpkg.OpenRouterConfig{BaseURL: "https://gateway.example/api", Models: []configpkg.OpenRouterModel{{ID: "vendor/model-x", Context: 1048576}}}
	got := open.AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{
		UseGateway:   &optOut,
		Models:       []string{"vendor/model-x"},
		DefaultModel: model("vendor/model-x"),
	}, catalog, "team", "dev")
	if strings.Contains(got, "CLAUDE_CODE_MAX_CONTEXT_TOKENS") {
		t.Fatalf("an opted-out environment must not take the catalog's context window, got %q", got)
	}
	if !strings.Contains(got, "CLAUDE_CODE_SUBAGENT_MODEL=vendor/model-x claude") {
		t.Fatalf("the environment's own model should still be mirrored to subagents, got %q", got)
	}
}

// Remote Control is off for Bedrock/Mantle gateways and non-claude tools, and
// unnamed when tenant/env is unsafe to inline.
func TestAISessionLaunchRemoteControl(t *testing.T) {
	t.Run("an unsafe tenant/env falls back to the unnamed flag", func(t *testing.T) {
		got := open.AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, nil, "a b; rm", "dev")
		if strings.Count(got, "--remote-control") != 1 || strings.Contains(got, "--remote-control ") {
			t.Fatalf("unsafe tenant must yield the unnamed flag, got %q", got)
		}
	})

	t.Run("a leading-dash tenant falls back to the unnamed flag", func(t *testing.T) {
		// Claude Code would parse a "-"-leading name as a flag.
		got := open.AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, nil, "-rm", "dev")
		if strings.Count(got, "--remote-control") != 1 || strings.Contains(got, "--remote-control ") {
			t.Fatalf("leading-dash tenant must yield the unnamed flag, got %q", got)
		}
	})

	t.Run("no remote control for a non-claude tool", func(t *testing.T) {
		if got := open.AISessionLaunchCommand("codex", configpkg.EnvironmentClaudeConfig{}, nil, "team", "dev"); strings.Contains(got, "--remote-control") {
			t.Fatalf("non-claude tool must launch verbatim without remote control, got %q", got)
		}
	})
}

func TestResolveClaudeLaunchModel(t *testing.T) {
	model := func(v string) *string { return &v }
	cases := []struct {
		name   string
		config configpkg.EnvironmentClaudeConfig
		want   string
	}{
		{"fable is opt-in: not auto-selected from the default available set", configpkg.EnvironmentClaudeConfig{DefaultModel: model("fable")}, "opus"},
		{"unsafe explicit model falls back to first available", configpkg.EnvironmentClaudeConfig{Models: []string{"opus"}, DefaultModel: model("a b; rm")}, "opus"},
		{"no safe token in the available set resolves to empty", configpkg.EnvironmentClaudeConfig{Models: []string{"a b; rm"}, DefaultModel: model("a b; rm")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := open.ResolveClaudeLaunchModel(tc.config, nil); got != tc.want {
				t.Fatalf("resolveClaudeLaunchModel(%+v) = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}

// A real claude exit cannot be staged in Playwright, so these assertions own
// the exit wrapper's contract.
func TestAISessionLaunchLines(t *testing.T) {
	t.Run("the wrapper executes: 137 yields the OOM marker and the intact resume", func(t *testing.T) {
		lines := open.AISessionLaunchLines("", configpkg.EnvironmentClaudeConfig{}, nil, "team", "dev")
		// Replace the launch with a 137 exit to simulate an OOM kill.
		lines[1] = "(exit 137) || ai_status=$?"
		out, err := exec.Command("sh", "-c", strings.Join(lines, "\n")).CombinedOutput()
		if err != nil {
			t.Fatalf("wrapper script failed: %v\n%s", err, out)
		}
		text := string(out)
		if !strings.Contains(text, "Claude was killed (exit 137)") {
			t.Fatalf("expected the OOM marker, got:\n%s", text)
		}
		if !strings.Contains(text, `resume with: CLAUDE_CODE_SUBAGENT_MODEL=opus claude --continue --settings '{"ultracode":true}' --model opus --remote-control team/dev`) {
			t.Fatalf("expected the resume command with its quotes intact, got:\n%s", text)
		}
	})

	t.Run("a verbatim tool keeps its own resume and a tool-neutral label", func(t *testing.T) {
		script := strings.Join(open.AISessionLaunchLines("codex", configpkg.EnvironmentClaudeConfig{}, nil, "team", "dev"), "\n")
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

func TestClaudeBackgroundEffortKeepsMaxAsAFlag(t *testing.T) {
	cases := []struct {
		effort   string
		settings map[string]any
		flags    []string
	}{
		{"low", map[string]any{"effortLevel": "low"}, nil},
		{"xhigh", map[string]any{"effortLevel": "xhigh"}, nil},
		{"max", map[string]any{}, []string{"--effort", "max"}},
		{"ultracode", map[string]any{"ultracode": true}, nil},
		{"bogus", map[string]any{}, nil},
	}
	for _, tc := range cases {
		settings, flags := ClaudeBackgroundEffort(tc.effort)
		if len(settings) != len(tc.settings) {
			t.Fatalf("%s: settings = %v, want %v", tc.effort, settings, tc.settings)
		}
		for key, want := range tc.settings {
			if settings[key] != want {
				t.Fatalf("%s: settings[%s] = %v, want %v", tc.effort, key, settings[key], want)
			}
		}
		if strings.Join(flags, " ") != strings.Join(tc.flags, " ") {
			t.Fatalf("%s: flags = %v, want %v", tc.effort, flags, tc.flags)
		}
	}
}
