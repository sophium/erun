package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeConfigDirEnv is the variable Claude Code itself honours for its settings directory.
const ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// hostGatewayCredentialEnvVars are in Claude Code's preference order. A blank
// ANTHROPIC_API_KEY is Claude Code's marker disabling its fallback, not a credential.
var hostGatewayCredentialEnvVars = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}

// ClaudeSettingsPath resolves the user-level Claude Code settings file.
func ClaudeSettingsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(ClaudeConfigDirEnv)); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// HostClaudeSettingsEnv returns the env block of this machine's Claude Code
// settings. A missing file is (nil, nil); a malformed one is tolerated as Claude
// Code itself tolerates it; an unreadable one is an error.
func HostClaudeSettingsEnv() (map[string]string, error) {
	path, err := ClaudeSettingsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return claudeSettingsEnv(raw), nil
}

func claudeSettingsEnv(raw []byte) map[string]string {
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil
	}
	return settings.Env
}

// GatewayCredentialFromEnv picks the gateway credential from a Claude Code settings env block.
func GatewayCredentialFromEnv(env map[string]string) (string, bool) {
	for _, name := range hostGatewayCredentialEnvVars {
		if value := strings.TrimSpace(env[name]); value != "" {
			return value, true
		}
	}
	return "", false
}

// HostClaudeGatewayCredential returns the host's Claude Code credential and gateway from one read.
func HostClaudeGatewayCredential() (token, endpoint string) {
	env, err := HostClaudeSettingsEnv()
	if err != nil {
		return "", ""
	}
	token, _ = GatewayCredentialFromEnv(env)
	return token, strings.TrimSpace(env["ANTHROPIC_BASE_URL"])
}

// SameGatewayEndpoint reports whether two addresses name the same gateway. It is
// deliberately strict: a looser match would reuse a credential across endpoints.
func SameGatewayEndpoint(a, b string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
	}
	left, right := normalize(a), normalize(b)
	return left != "" && left == right
}
