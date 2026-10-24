package main

import (
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// uiHostGatewayDefaults is the gateway the operator's Claude Code already uses.
type uiHostGatewayDefaults struct {
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
	// Context is zero when the settings declare none.
	Context int `json:"context,omitempty"`
	// No credential field: reuse depends on the endpoint check in
	// LoadGatewayCredentialStatus.
}

// LoadHostGatewayDefaults reports the gateway this machine's Claude Code routes
// through; no settings or no base URL is not an error, an unreadable file is.
func (a *App) LoadHostGatewayDefaults() (uiHostGatewayDefaults, error) {
	env, err := deploy.HostClaudeSettingsEnv()
	if err != nil {
		return uiHostGatewayDefaults{}, err
	}
	return hostGatewayDefaultsFromEnv(env), nil
}

func hostGatewayDefaultsFromEnv(env map[string]string) uiHostGatewayDefaults {
	baseURL := strings.TrimSpace(env["ANTHROPIC_BASE_URL"])
	if baseURL == "" {
		return uiHostGatewayDefaults{}
	}
	out := uiHostGatewayDefaults{BaseURL: baseURL}
	// The subagent model mirrors the primary in a managed launch.
	if model := strings.TrimSpace(env["ANTHROPIC_MODEL"]); model != "" {
		out.Model = model
		if context, err := strconv.Atoi(strings.TrimSpace(env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])); err == nil && context > 0 {
			out.Context = context
		}
	}
	return out
}

// credentialHint must be strictly shorter than the value, or it reveals it.
func credentialHint(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= credentialHintLength {
		return ""
	}
	return "…" + trimmed[len(trimmed)-credentialHintLength:]
}

const credentialHintLength = 4
