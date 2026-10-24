package ai

import (
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// HostClaudeGatewayEndpoint reports the gateway this machine's own Claude Code
// routes through, or "" when it routes through none.
//
// It exists to name the mismatch: a key that will not be reused should say which
// endpoint it belongs to rather than reading as no key at all.
func HostClaudeGatewayEndpoint() string {
	_, endpoint := deploy.HostClaudeGatewayCredential()
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
	token, endpoint := deploy.HostClaudeGatewayCredential()
	if token == "" || !deploy.SameGatewayEndpoint(endpoint, baseURL) {
		return "", false
	}
	return token, true
}
