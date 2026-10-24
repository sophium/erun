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

// The runtime pod relays the routing values the chart injects into Claude Code's
// settings.json, wires the erun MCP server into ~/.claude.json, and installs the
// AI-session hooks and the permission mode the pod runs Claude Code under.
//
// Bedrock and a gateway both route Claude through a provider the environment
// selects rather than a claude.ai login, so the same relay applies to both. The
// chart emits ANTHROPIC_BASE_URL only when a gateway is configured, which makes
// the environment itself the signal. Only non-secret routing values are relayed;
// a credential stays in the pod environment through its Secret reference.

const claudeSettingsSchema = "https://json.schemastore.org/claude-code-settings.json"

// claudeRelayedPassthroughNames are relayed verbatim when set. A gateway serves
// model ids Claude Code cannot size, so the context window the chart declares for
// the catalog's default travels too.
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

// claudeRelayedEnvNames is every settings env entry the relay can write.
var claudeRelayedEnvNames = append([]string{
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_MANTLE",
	"AWS_REGION",
	"ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION",
	"CLAUDE_CODE_MAX_OUTPUT_TOKENS",
	"MAX_THINKING_TOKENS",
}, claudeRelayedPassthroughNames...)

// claudeRelayRecord is what the relay wrote on its last boot, kept beside the
// settings file. settings.json lives on the persistent volume, so a value relayed
// while the environment used a gateway or Bedrock would otherwise outlive the
// deploy that turned it off; the record is how a later boot removes exactly what
// it wrote and never an entry the operator wrote by hand.
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

// relayRuntimeClaudeSettings reconciles settings.json and its relay record.
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

// resolveOwnership reads what the relay owns. A pod with no record predates it:
// if its earlier relay evidently ran (it always wrote both token defaults), every
// entry that relay wrote was one of claudeRelayedEnvNames, so all of them count as
// owned once; a settings file the relay never touched owns nothing.
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

// clear removes every entry the relay still owns: nothing routes through a
// relay now, so each is a leftover from a deploy that did.
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

// applyUseFlags writes the two flags Claude Code reads as present-or-absent:
// writing "0" would enable them, so a disabled flag is removed instead.
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

// claudeModelList splits the chart's comma-separated model list, dropping blanks
// and case-insensitive duplicates.
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

// installRuntimeClaudeSessionSettings runs Claude Code in bypass-permissions mode
// (the pod is the sandbox) and installs the AI-session hooks. The hooks are the
// writer the structured AI-session status model needs: without them it resolves
// from events nothing reports, and awaiting-input is unreachable. A previous
// boot's hook block is replaced, and an operator's own hook on the same event is
// kept beside it.
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

// runtimeAISessionHookCommand is one installed report. The environment check is
// in the shell so a Claude the operator runs with no erun environment in scope
// forks nothing on every turn boundary; nothing it prints or exits with reaches
// the turn.
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

// wireRuntimeClaudeMCP registers the environment's MCP edge for the repository
// project in ~/.claude.json.
func wireRuntimeClaudeMCP(statePath, projectPath, mcpURL string) error {
	state, err := readRuntimeJSONMap(statePath)
	if err != nil {
		return err
	}
	project := runtimeJSONObject(runtimeJSONObject(state, "projects"), projectPath)
	runtimeJSONObject(project, "mcpServers")["erun"] = map[string]any{"type": "http", "url": mcpURL}
	return writeRuntimeJSONMap(statePath, state)
}

// readRuntimeJSONMap reads a JSON object, keeping numbers as written. A missing
// or unparsable file reads as an empty object, as the shell relay always treated
// it.
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
	if decoder.Decode(&object) != nil || object == nil {
		return map[string]any{}, nil
	}
	return object, nil
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

// runtimeJSONObject returns parent[name] as an object, replacing a value that is
// not one.
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
