package podruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// Only non-secret routing values are relayed into settings.json; credentials stay in the pod
// environment. ANTHROPIC_BASE_URL is present only when a gateway is configured.

const claudeSettingsSchema = "https://json.schemastore.org/claude-code-settings.json"

// A gateway serves model ids Claude Code cannot size, so the context window is relayed too.
var claudeRelayedPassthroughNames = []string{
	"AWS_PROFILE",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_BEDROCK_BASE_URL",
	"ANTHROPIC_BEDROCK_MANTLE_BASE_URL",
	"ANTHROPIC_BEDROCK_SERVICE_TIER",
	"CLAUDE_CODE_MAX_CONTEXT_TOKENS",
	"CLAUDE_CODE_SKIP_MANTLE_AUTH",
	"DISABLE_PROMPT_CACHING",
	"ENABLE_PROMPT_CACHING_1H",
}

var claudeRelayedEnvNames = append([]string{
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_MANTLE",
	"AWS_REGION",
	"ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION",
	"CLAUDE_CODE_MAX_OUTPUT_TOKENS",
	"MAX_THINKING_TOKENS",
}, claudeRelayedPassthroughNames...)

// claudeRelayRecord lets a later boot remove exactly what the relay wrote to the
// persistent settings.json, never the operator's own entries.
type claudeRelayRecord struct {
	Env             []string `json:"env"`
	AvailableModels bool     `json:"availableModels"`
}

type claudeSettingsRelay struct {
	env             func(string) string
	settings        map[string]any
	ownedEnv        map[string]bool
	ownsModels      bool
	relayedNow      claudeRelayRecord
	configureRelay  bool
	region          string
	availableModels []string
}

func configureRuntimeClaude(boot runtimeBoot) error {
	settingsPath := boot.path(".claude", "settings.json")
	if err := relayRuntimeClaudeSettings(boot.env, settingsPath); err != nil {
		return fmt.Errorf("claude settings: %w", err)
	}
	statePath := boot.path(".claude.json")
	if err := wireRuntimeClaudeMCP(statePath, boot.repoDir(), boot.mcpURL()); err != nil {
		return fmt.Errorf("claude state: %w", err)
	}
	return nil
}

func relayRuntimeClaudeSettings(env func(string) string, settingsPath string) error {
	settings, err := readRuntimeJSONMap(settingsPath)
	if err != nil {
		return err
	}
	relay := newClaudeSettingsRelay(env, settings, settingsPath+".erun-relayed.json")
	relay.apply()
	installRuntimeClaudeSessionSettings(settings)
	if err := writeRuntimeJSONMap(settingsPath, settings); err != nil {
		return err
	}
	return writeRuntimeJSON(settingsPath+".erun-relayed.json", relay.relayedNow)
}

func newClaudeSettingsRelay(env func(string) string, settings map[string]any, recordPath string) *claudeSettingsRelay {
	relay := &claudeSettingsRelay{env: env, settings: settings, ownedEnv: map[string]bool{}, relayedNow: claudeRelayRecord{Env: []string{}}}
	relay.region = core.FirstNonEmptyString(relay.get("AWS_REGION"), relay.get("ERUN_CLOUD_REGION"))
	provider := relay.get("ERUN_CLOUD_PROVIDER")
	if provider == "" && strings.HasSuffix(relay.get("ERUN_CLOUD_PROVIDER_ALIAS"), "@aws") {
		provider = "aws"
	}
	bedrock := (provider == "aws" || relay.get("CLAUDE_CODE_USE_BEDROCK") != "" || relay.get("CLAUDE_CODE_USE_MANTLE") != "") && relay.region != ""
	relay.configureRelay = bedrock || relay.get("ANTHROPIC_BASE_URL") != ""
	relay.availableModels = claudeModelList(relay.get("ERUN_CLAUDE_AVAILABLE_MODELS"))
	relay.resolveOwnership(recordPath)
	return relay
}

func (r *claudeSettingsRelay) get(name string) string {
	return strings.TrimSpace(r.env(name))
}

// A pod with no record whose earlier relay evidently ran (both token defaults present)
// owns all claudeRelayedEnvNames once.
func (r *claudeSettingsRelay) resolveOwnership(recordPath string) {
	if data, err := os.ReadFile(recordPath); err == nil {
		var record claudeRelayRecord
		_ = json.Unmarshal(data, &record)
		for _, name := range record.Env {
			r.ownedEnv[name] = true
		}
		r.ownsModels = record.AvailableModels
		return
	}
	env, _ := r.settings["env"].(map[string]any)
	if runtimeJSONString(env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"]) == "" || runtimeJSONString(env["MAX_THINKING_TOKENS"]) == "" {
		return
	}
	for _, name := range claudeRelayedEnvNames {
		r.ownedEnv[name] = true
	}
	r.ownsModels = true
}

func (r *claudeSettingsRelay) apply() {
	if !r.configureRelay {
		r.clear()
		return
	}
	if _, ok := r.settings["$schema"]; !ok {
		r.settings["$schema"] = claudeSettingsSchema
	}
	env := runtimeJSONObject(r.settings, "env")
	r.applyUseFlags(env)
	r.sync(env, "AWS_REGION", r.region)
	r.sync(env, "ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION", core.FirstNonEmptyString(r.get("ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION"), r.region))
	r.sync(env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS", core.FirstNonEmptyString(r.get("CLAUDE_CODE_MAX_OUTPUT_TOKENS"), "32000"))
	r.sync(env, "MAX_THINKING_TOKENS", core.FirstNonEmptyString(r.get("MAX_THINKING_TOKENS"), "1024"))
	for _, name := range claudeRelayedPassthroughNames {
		r.sync(env, name, r.get(name))
	}
	switch {
	case len(r.availableModels) > 0:
		r.settings["availableModels"] = r.availableModels
		r.relayedNow.AvailableModels = true
	case r.ownsModels:
		delete(r.settings, "availableModels")
	}
}

func (r *claudeSettingsRelay) clear() {
	if env, ok := r.settings["env"].(map[string]any); ok {
		for _, name := range claudeRelayedEnvNames {
			r.sync(env, name, "")
		}
	}
	if r.ownsModels {
		delete(r.settings, "availableModels")
	}
}

// Claude Code reads these flags as present-or-absent: "0" would enable them.
func (r *claudeSettingsRelay) applyUseFlags(env map[string]any) {
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_MANTLE"} {
		if !runtimeEnvFlagEnabled(r.get(name)) {
			delete(env, name)
			continue
		}
		env[name] = "1"
		r.relayedNow.Env = append(r.relayedNow.Env, name)
	}
}

func (r *claudeSettingsRelay) sync(env map[string]any, name, value string) {
	if value = strings.TrimSpace(value); value != "" {
		env[name] = value
		r.relayedNow.Env = append(r.relayedNow.Env, name)
		return
	}
	if r.ownedEnv[name] {
		delete(env, name)
	}
}

func claudeModelList(value string) []string {
	var models []string
	seen := map[string]bool{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		key := strings.ToLower(entry)
		if entry == "" || seen[key] {
			continue
		}
		seen[key] = true
		models = append(models, entry)
	}
	return models
}

// installRuntimeClaudeSessionSettings replaces the previous boot's hooks and keeps the
// operator's own hooks on the same event. The pod is the sandbox.
func installRuntimeClaudeSessionSettings(settings map[string]any) {
	if _, ok := settings["$schema"]; !ok {
		settings["$schema"] = claudeSettingsSchema
	}
	runtimeJSONObject(settings, "permissions")["defaultMode"] = "bypassPermissions"
	settings["skipDangerousModePermissionPrompt"] = true
	hooks := runtimeJSONObject(settings, "hooks")
	for _, binding := range AISessionHookBindings() {
		current, _ := hooks[binding.ToolEvent].([]any)
		blocks := make([]any, 0, len(current)+1)
		for _, block := range current {
			if !isRuntimeAISessionHookBlock(block) {
				blocks = append(blocks, block)
			}
		}
		blocks = append(blocks, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": runtimeAISessionHookCommand(binding.ModelEvent)}}})
		hooks[binding.ToolEvent] = blocks
	}
}

// The shell-side environment check keeps a Claude run outside erun from forking on every turn.
func runtimeAISessionHookCommand(event model.AISessionEventKind) string {
	return `if [ -n "${ERUN_TENANT}" ] && [ -n "${ERUN_ENVIRONMENT}" ]; then ` + AISessionHookVerbPath + " --event " + string(event) + " --tool claude >/dev/null 2>&1 || true; fi"
}

func isRuntimeAISessionHookBlock(block any) bool {
	object, _ := block.(map[string]any)
	entries, _ := object["hooks"].([]any)
	for _, entry := range entries {
		hook, _ := entry.(map[string]any)
		if command, ok := hook["command"].(string); ok && strings.Contains(command, "activity ai-session hook") {
			return true
		}
	}
	return false
}

func wireRuntimeClaudeMCP(statePath, projectPath, mcpURL string) error {
	state, err := readRuntimeJSONMap(statePath)
	if err != nil {
		return err
	}
	project := runtimeJSONObject(runtimeJSONObject(state, "projects"), projectPath)
	runtimeJSONObject(project, "mcpServers")["erun"] = map[string]any{"type": "http", "url": mcpURL}
	return writeRuntimeJSONMap(statePath, state)
}

// A missing or unparsable file reads as an empty object.
func readRuntimeJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	object := map[string]any{}
	if decoder.Decode(&object) == nil && object != nil {
		return object, nil
	}
	return map[string]any{}, nil
}

func writeRuntimeJSONMap(path string, value map[string]any) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(path, buffer.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func runtimeJSONObject(parent map[string]any, name string) map[string]any {
	if object, ok := parent[name].(map[string]any); ok {
		return object
	}
	object := map[string]any{}
	parent[name] = object
	return object
}

func runtimeJSONString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
