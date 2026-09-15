package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// claudeSettingsDirEnv overrides where Claude Code keeps its user settings. The
// desktop honours it for the same reason Claude Code does: an operator who has
// moved the directory should see their own configuration, not a path we assumed.
const claudeSettingsDirEnv = "CLAUDE_CONFIG_DIR"

// uiHostGatewayDefaults is the gateway the operator's own Claude Code is already
// pointed at, read from their user-level settings.
//
// It exists so a catalog can start from what this machine already runs, rather
// than asking the operator to retype an endpoint and model they configured
// once already.
//
// The credential is deliberately absent. Settings hold the token as a *value*,
// while the catalog carries a Secret *name* the pod resolves — so there is
// nothing to carry across, and reading it would pull a credential into the
// desktop's read model for no gain.
type uiHostGatewayDefaults struct {
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
	// Context is the window the settings declare for Model. Zero means they
	// declare none, so the catalog leaves the field to the operator.
	Context int `json:"context,omitempty"`
}

// LoadHostGatewayDefaults reports the gateway this machine's Claude Code already
// routes through, if it routes through one at all.
//
// No settings file, or one that names no base URL, yields no defaults rather
// than an error: an install that has never configured a gateway has nothing to
// offer, which is the ordinary case rather than a failure. A file that is
// present but unreadable is reported, because that is a real fault worth seeing.
func (a *App) LoadHostGatewayDefaults() (uiHostGatewayDefaults, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return uiHostGatewayDefaults{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return uiHostGatewayDefaults{}, nil
		}
		return uiHostGatewayDefaults{}, err
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		// Claude Code tolerates a malformed user settings file by ignoring the
		// entries it cannot read, so a desktop that refused to open its catalog
		// over one would be stricter than the tool owning the file.
		return uiHostGatewayDefaults{}, nil
	}
	return hostGatewayDefaultsFromEnv(settings.Env), nil
}

func hostGatewayDefaultsFromEnv(env map[string]string) uiHostGatewayDefaults {
	baseURL := strings.TrimSpace(env["ANTHROPIC_BASE_URL"])
	if baseURL == "" {
		return uiHostGatewayDefaults{}
	}
	out := uiHostGatewayDefaults{BaseURL: baseURL}
	// The primary model is what a catalog would offer first; the subagent model
	// mirrors it in a managed launch, so it adds nothing when it agrees.
	if model := strings.TrimSpace(env["ANTHROPIC_MODEL"]); model != "" {
		out.Model = model
		if context, err := strconv.Atoi(strings.TrimSpace(env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])); err == nil && context > 0 {
			out.Context = context
		}
	}
	return out
}

// claudeSettingsPath resolves the user-level Claude Code settings file. The
// config directory is honoured when set, so an operator who has moved it is
// read where they actually keep it.
func claudeSettingsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(claudeSettingsDirEnv)); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}
