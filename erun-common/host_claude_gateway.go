package eruncommon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// claudeConfigDirEnv overrides where Claude Code keeps its user settings. It is
// the same variable Claude Code itself honours, so an operator who has moved
// the directory is read where they actually keep it.
const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// hostGatewayCredentialEnvVars are the settings entries a gateway credential can
// live in, in the order Claude Code prefers them.
//
// ANTHROPIC_AUTH_TOKEN is the bearer token a gateway expects. ANTHROPIC_API_KEY
// is the x-api-key path, and a blank one is a deliberate marker — Claude Code
// uses "" to disable its direct-provider fallback — so a blank is skipped
// rather than read as a credential.
var hostGatewayCredentialEnvVars = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}

// ClaudeSettingsPath resolves the user-level Claude Code settings file.
func ClaudeSettingsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(claudeConfigDirEnv)); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// HostClaudeSettingsEnv reads this machine's own Claude Code settings and
// returns the env block it declares.
//
// It is the one read of that file, so everything derived from it — the gateway
// defaults offered to a catalog, and the credential those settings carry —
// comes from the same snapshot and cannot disagree with itself.
//
// A missing file is (nil, nil): an install that never configured Claude Code has
// nothing to offer, which is the ordinary case rather than a fault. A file that
// is present but unreadable is an error, because that is a real fault worth
// seeing. A malformed file is tolerated — Claude Code ignores the entries it
// cannot read, so erun reading the same file more strictly than the tool that
// owns it would report problems that tool does not have.
func HostClaudeSettingsEnv() (map[string]string, error) {
	path, err := ClaudeSettingsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, nil
	}
	return settings.Env, nil
}

// GatewayCredentialFromEnv picks the gateway credential out of a Claude Code
// settings env block, in the order Claude Code prefers.
func GatewayCredentialFromEnv(env map[string]string) (string, bool) {
	for _, name := range hostGatewayCredentialEnvVars {
		if value := strings.TrimSpace(env[name]); value != "" {
			return value, true
		}
	}
	return "", false
}

// HostClaudeGatewayCredential returns the gateway credential this machine's own
// Claude Code authenticates with, and whether it found one.
//
// It is where a catalog's credential starts. An operator who already runs Claude
// Code against a gateway has the key on this machine, so asking them to paste it
// again would be asking for something erun can read.
//
// Every failure reads as "no credential" rather than an error: this supplies a
// default, and a settings file that is absent or unreadable is the unconfigured
// case here, not a fault worth failing a deploy over. The value is returned to
// whoever stores or delivers it and never lands in config.yaml, a chart value,
// or a launch command.
func HostClaudeGatewayCredential() (string, bool) {
	env, err := HostClaudeSettingsEnv()
	if err != nil {
		return "", false
	}
	return GatewayCredentialFromEnv(env)
}
