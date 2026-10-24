package podruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestRelayRuntimeClaudeSettingsWritesNoRoutingWithoutARelay(t *testing.T) {
	_, raw := relayTestSettings(t, nil, "")
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "AWS_REGION", "availableModels"} {
		if strings.Contains(raw, name) {
			t.Fatalf("no %s should be relayed without a gateway:\n%s", name, raw)
		}
	}
}

func TestRelayRuntimeClaudeSettingsRelaysBedrock(t *testing.T) {
	settings, _ := relayTestSettings(t, map[string]string{
		"ERUN_CLOUD_PROVIDER_ALIAS": "operator@aws", "ERUN_CLOUD_REGION": "eu-west-2",
		"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_MANTLE": "0",
	}, `{"env": {"CLAUDE_CODE_USE_MANTLE": "0"}}`)
	env := testJSONObject(t, settings["env"])
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" || env["AWS_REGION"] != "eu-west-2" || env["ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION"] != "eu-west-2" ||
		env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] != "32000" || env["MAX_THINKING_TOKENS"] != "1024" {
		t.Fatalf("unexpected Bedrock relay: %v", env)
	}
	if _, ok := env["CLAUDE_CODE_USE_MANTLE"]; ok {
		t.Fatalf("a disabled use flag must be removed, not written as 0: %v", env)
	}
}

// This pod predates the ownership record but both token defaults show its relay ran.
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

func TestRuntimeClaudeSessionSettingsInstallEveryAISessionHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeAssetFile(t, path, `{"permissions": {"allow": ["Bash"]}, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "printf 'operator hook\n'"}]}]}}`)
	for i := 0; i < 2; i++ {
		if err := relayRuntimeClaudeSettings(func(string) string { return "" }, path); err != nil {
			t.Fatal(err)
		}
	}
	settings := readTestJSON(t, path)
	permissions := testJSONObject(t, settings["permissions"])
	if permissions["defaultMode"] != "bypassPermissions" || permissions["allow"] == nil || settings["skipDangerousModePermissionPrompt"] != true {
		t.Fatalf("unexpected permission settings: %v", settings)
	}
	hooks := testJSONObject(t, settings["hooks"])
	for _, binding := range AISessionHookBindings() {
		ours := runtimeAISessionHookCommands(t, testJSONArray(t, hooks[binding.ToolEvent]))
		if len(ours) != 1 {
			t.Fatalf("%s should carry exactly one AI-session report, got %d", binding.ToolEvent, len(ours))
		}
		if !strings.Contains(ours[0], AISessionHookVerbPath+" --event "+string(binding.ModelEvent)+" --tool claude") {
			t.Fatalf("%s should report %s, got %q", binding.ToolEvent, binding.ModelEvent, ours[0])
		}
	}
	if len(testJSONArray(t, hooks["Stop"])) != 2 {
		t.Fatalf("the operator's own Stop hook must be kept beside ours: %v", hooks["Stop"])
	}
}

func runtimeAISessionHookCommands(t *testing.T, blocks []any) []string {
	t.Helper()
	var commands []string
	for _, block := range blocks {
		if isRuntimeAISessionHookBlock(block) {
			hook := testJSONObject(t, testJSONArray(t, testJSONObject(t, block)["hooks"])[0])
			command, ok := hook["command"].(string)
			if !ok {
				t.Fatalf("want a hook command string, got %T", hook["command"])
			}
			commands = append(commands, command)
		}
	}
	return commands
}
