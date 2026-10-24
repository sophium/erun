package podruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/orchestration"
)

func relayTestSettings(t *testing.T, env map[string]string, existing string) (map[string]any, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if existing != "" {
		writeAssetFile(t, path, existing)
	}
	if err := relayRuntimeClaudeSettings(func(key string) string { return env[key] }, path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	return readTestJSON(t, path), string(data)
}

// A configured gateway relays the model list (which makes the catalog
// selectable), the default model and the model's context window into settings;
// the credential stays in the pod environment and never reaches a file.
func TestRelayRuntimeClaudeSettingsRelaysAGatewaysRouting(t *testing.T) {
	_, raw := relayTestSettings(t, map[string]string{
		"ANTHROPIC_BASE_URL":             "https://openrouter.ai/api",
		"ANTHROPIC_AUTH_TOKEN":           "secret",
		"ANTHROPIC_MODEL":                "deepseek/deepseek-v4.1-flash",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1048576",
		"ERUN_CLAUDE_AVAILABLE_MODELS":   "anthropic/claude-fable-5.1, deepseek/deepseek-v4.1-flash,Anthropic/Claude-Fable-5.1",
	}, "")
	for _, want := range []string{"claude-fable-5.1", `"ANTHROPIC_MODEL": "deepseek/deepseek-v4.1-flash"`, "1048576"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("settings should carry %s:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "ANTHROPIC_AUTH_TOKEN") || strings.Contains(raw, "secret") {
		t.Fatalf("the credential must never be written into settings:\n%s", raw)
	}
	if strings.Count(raw, "laude-fable-5.1") != 1 {
		t.Fatalf("the model list should drop case-insensitive duplicates:\n%s", raw)
	}
}

// An install with no gateway and no Bedrock must land in exactly the settings
// shape it had, apart from the session settings every pod gets.
func TestRelayRuntimeClaudeSettingsWritesNoRoutingWithoutARelay(t *testing.T) {
	_, raw := relayTestSettings(t, nil, "")
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "AWS_REGION", "availableModels"} {
		if strings.Contains(raw, name) {
			t.Fatalf("no %s should be relayed without a gateway:\n%s", name, raw)
		}
	}
}

// Bedrock is relayed for an AWS environment with a region; the use flags are
// present-or-absent to Claude Code, so a disabled one is removed, never "0".
func TestRelayRuntimeClaudeSettingsRelaysBedrock(t *testing.T) {
	settings, _ := relayTestSettings(t, map[string]string{
		"ERUN_CLOUD_PROVIDER_ALIAS": "operator@aws", "ERUN_CLOUD_REGION": "eu-west-2",
		"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_MANTLE": "0",
	}, `{"env": {"CLAUDE_CODE_USE_MANTLE": "0"}}`)
	env := settings["env"].(map[string]any)
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" || env["AWS_REGION"] != "eu-west-2" || env["ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION"] != "eu-west-2" ||
		env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] != "32000" || env["MAX_THINKING_TOKENS"] != "1024" {
		t.Fatalf("unexpected Bedrock relay: %v", env)
	}
	if _, ok := env["CLAUDE_CODE_USE_MANTLE"]; ok {
		t.Fatalf("a disabled use flag must be removed, not written as 0: %v", env)
	}
}

// settings.json lives on the persistent volume, so a pod redeployed off the
// gateway would otherwise keep launching the gateway's model. This pod predates
// the ownership record: its earlier relay evidently ran (both token defaults are
// there), so what it wrote goes and what the operator wrote stays.
func TestRelayRuntimeClaudeSettingsRemovesWhatItRelayedAfterLeavingTheGateway(t *testing.T) {
	_, raw := relayTestSettings(t, nil, `{
  "theme": "dark",
  "env": {
    "ANTHROPIC_MODEL": "deepseek/deepseek-v4.1-flash",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1048576",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "32000",
    "MAX_THINKING_TOKENS": "1024",
    "OPERATOR_OWN_SETTING": "keep"
  },
  "availableModels": ["deepseek/deepseek-v4.1-flash"]
}`)
	for _, stale := range []string{"ANTHROPIC_MODEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "availableModels", "deepseek"} {
		if strings.Contains(raw, stale) {
			t.Fatalf("leaving the gateway should remove the relayed %s:\n%s", stale, raw)
		}
	}
	for _, kept := range []string{"OPERATOR_OWN_SETTING", `"theme"`} {
		if !strings.Contains(raw, kept) {
			t.Fatalf("the relay must not remove %s, which it never wrote:\n%s", kept, raw)
		}
	}
}

// A settings file the relay never touched owns nothing, and once the record
// exists an entry the relay did not write survives even under a name it could
// own.
func TestRelayRuntimeClaudeSettingsNeverRemovesTheOperatorsOwnEntries(t *testing.T) {
	_, raw := relayTestSettings(t, nil, `{"env": {"ANTHROPIC_MODEL": "sonnet", "MAX_THINKING_TOKENS": "4096"}, "availableModels": ["sonnet"]}`)
	for _, kept := range []string{`"ANTHROPIC_MODEL": "sonnet"`, `"MAX_THINKING_TOKENS": "4096"`, "availableModels"} {
		if !strings.Contains(raw, kept) {
			t.Fatalf("a settings file the relay never wrote must keep %s:\n%s", kept, raw)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeAssetFile(t, path+".erun-relayed.json", `{"env": [], "availableModels": false}`)
	writeAssetFile(t, path, `{"env": {"ANTHROPIC_MODEL": "opus"}}`)
	if err := relayRuntimeClaudeSettings(func(string) string { return "" }, path); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"ANTHROPIC_MODEL": "opus"`) {
		t.Fatalf("an operator's own ANTHROPIC_MODEL must survive once the relay records what it owns:\n%s", data)
	}
}

// The hooks are the writer the AI-session status model needs: every binding is
// installed with the event it reports, a reboot replaces its own block rather
// than stacking another, and an operator's own hook on the same event is kept.
func TestRuntimeClaudeSessionSettingsInstallEveryAISessionHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeAssetFile(t, path, `{"permissions": {"allow": ["Bash"]}, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "printf 'operator hook\n'"}]}]}}`)
	for i := 0; i < 2; i++ {
		if err := relayRuntimeClaudeSettings(func(string) string { return "" }, path); err != nil {
			t.Fatal(err)
		}
	}
	settings := readTestJSON(t, path)
	permissions := settings["permissions"].(map[string]any)
	if permissions["defaultMode"] != "bypassPermissions" || permissions["allow"] == nil || settings["skipDangerousModePermissionPrompt"] != true {
		t.Fatalf("unexpected permission settings: %v", settings)
	}
	hooks := settings["hooks"].(map[string]any)
	for _, binding := range orchestration.AISessionHookBindings() {
		ours := runtimeAISessionHookCommands(hooks[binding.ToolEvent].([]any))
		if len(ours) != 1 {
			t.Fatalf("%s should carry exactly one AI-session report, got %d", binding.ToolEvent, len(ours))
		}
		if !strings.Contains(ours[0], orchestration.AISessionHookVerbPath+" --event "+string(binding.ModelEvent)+" --tool claude") {
			t.Fatalf("%s should report %s, got %q", binding.ToolEvent, binding.ModelEvent, ours[0])
		}
	}
	if len(hooks["Stop"].([]any)) != 2 {
		t.Fatalf("the operator's own Stop hook must be kept beside ours: %v", hooks["Stop"])
	}
}

func runtimeAISessionHookCommands(blocks []any) []string {
	var commands []string
	for _, block := range blocks {
		if isRuntimeAISessionHookBlock(block) {
			commands = append(commands, block.(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string))
		}
	}
	return commands
}
