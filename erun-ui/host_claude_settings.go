package main

import (
	"strconv"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// uiHostGatewayDefaults is the gateway the operator's own Claude Code is already
// pointed at, read from their user-level settings.
//
// It exists so a catalog can start from what this machine already runs, rather
// than asking the operator to retype an endpoint and model they configured
// once already.
//
// The credential travels as a hint and a presence flag, never as the value. The
// desktop has no use for a live token — deploy reads it host-side — and a read
// model is the wrong place to carry one.
type uiHostGatewayDefaults struct {
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
	// Context is the window the settings declare for Model. Zero means they
	// declare none, so the catalog leaves the field to the operator.
	Context int `json:"context,omitempty"`
	// There is deliberately nothing about the credential here. Whether a key can
	// be reused depends on which gateway it belongs to, and this type describes
	// a gateway being *offered* — so a second, ungated claim about the key would
	// be a second source of truth. The catalog asks
	// LoadGatewayCredentialStatus, which applies the endpoint check.
}

// LoadHostGatewayDefaults reports the gateway this machine's Claude Code already
// routes through, if it routes through one at all.
//
// No settings file, or one that names no base URL, yields no defaults rather
// than an error: an install that has never configured a gateway has nothing to
// offer, which is the ordinary case rather than a failure. A file that is
// present but unreadable is reported, because that is a real fault worth seeing.
func (a *App) LoadHostGatewayDefaults() (uiHostGatewayDefaults, error) {
	env, err := eruncommon.HostClaudeSettingsEnv()
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

// credentialHint identifies a credential without revealing it. Four characters
// is enough to tell two keys apart and far too few to use one.
//
// The value must be strictly longer than the suffix: at exactly that length the
// "hint" would be the whole credential, which is the opposite of the point.
func credentialHint(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= credentialHintLength {
		return ""
	}
	return "…" + trimmed[len(trimmed)-credentialHintLength:]
}

const credentialHintLength = 4
