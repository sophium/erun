package ai

import (
	"github.com/sophium/erun/erun-common/primitive/deploy/gateway"
)

// HostClaudeGatewayEndpoint returns the gateway the host's Claude Code uses,
// or "", so a refusal can name the mismatch.
func HostClaudeGatewayEndpoint() string {
	_, endpoint := gateway.HostClaudeGatewayCredential()
	return endpoint
}

// HostClaudeGatewayCredentialFor returns the host Claude Code's credential only if
// it routes through the same gateway as baseURL, so a key never reaches another
// service. Failures mean "none"; the value must never land in config or a launch command.
func HostClaudeGatewayCredentialFor(baseURL string) (string, bool) {
	token, endpoint := gateway.HostClaudeGatewayCredential()
	if token == "" || !gateway.SameGatewayEndpoint(endpoint, baseURL) {
		return "", false
	}
	return token, true
}
