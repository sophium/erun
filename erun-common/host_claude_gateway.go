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

// hostClaudeGatewayCredential describes what this machine's own Claude Code
// holds: the credential it authenticates with, if any, and the gateway it is
// pointed at, if any.
//
// One read answers both, so a caller deciding whether a key may be reused — and
// a caller explaining why it may not — cannot reach two different conclusions
// about the same settings file.
func hostClaudeGatewayCredential() (token, endpoint string) {
	env, err := HostClaudeSettingsEnv()
	if err != nil {
		return "", ""
	}
	token, _ = GatewayCredentialFromEnv(env)
	return token, strings.TrimSpace(env["ANTHROPIC_BASE_URL"])
}

// HostClaudeGatewayEndpoint reports the gateway this machine's own Claude Code
// routes through, or "" when it routes through none.
//
// It exists to name the mismatch: a key that will not be reused should say which
// endpoint it belongs to rather than reading as no key at all.
func HostClaudeGatewayEndpoint() string {
	_, endpoint := hostClaudeGatewayCredential()
	return endpoint
}

// HostClaudeGatewayCredentialFor returns the credential this machine's own
// Claude Code authenticates with, and whether it may be reused for the gateway
// at baseURL.
//
// The endpoint must match, and that check is the whole point of this function.
// A credential is scoped to the service it was issued for, so reusing the host's
// key against a different gateway would hand a credential minted for one service
// to another: an operator whose Claude Code authenticates directly against
// Anthropic carries an ANTHROPIC_API_KEY in these very settings, and delivering
// that as a gateway's bearer token would disclose it to that gateway. The
// fallback therefore covers exactly the case it was written for — an operator
// already running Claude Code through this same gateway — and declines every
// other, leaving the operator to save the gateway's own key.
//
// Every failure reads as "no credential" rather than an error: this supplies a
// default, and a settings file that is absent or unreadable is the unconfigured
// case here, not a fault worth failing a deploy over. The value is returned to
// whoever stores or delivers it and never lands in config.yaml, a chart value,
// or a launch command.
func HostClaudeGatewayCredentialFor(baseURL string) (string, bool) {
	token, endpoint := hostClaudeGatewayCredential()
	if token == "" || !sameGatewayEndpoint(endpoint, baseURL) {
		return "", false
	}
	return token, true
}

// sameGatewayEndpoint reports whether two addresses name the same gateway.
//
// Matching is deliberately strict — only surrounding whitespace, a trailing
// slash, and case differ freely — because the permissive direction is the unsafe
// one: a looser comparison would reuse a credential across endpoints that merely
// look alike. A gateway spelled differently enough to fail this simply declines
// the fallback, and the operator saves its key instead.
func sameGatewayEndpoint(a, b string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
	}
	left, right := normalize(a), normalize(b)
	return left != "" && left == right
}
