package callerid

import (
	"context"
	"log"

	"github.com/sophium/erun/erun-common/infra/mcptools"
)

// Tools are gated twice: per-request registration hides what a caller cannot
// use, and each handler re-checks at call time.

// AuthIdentity is the resolved caller.
type AuthIdentity struct {
	Tenant       string
	User         string
	Capabilities mcptools.MCPCapabilitySet
}

type AuthContextKey struct{}

func WithAuthIdentity(ctx context.Context, identity AuthIdentity) context.Context {
	return context.WithValue(ctx, AuthContextKey{}, identity)
}

// AuthIdentityFrom returns the resolved caller; an unauthenticated edge resolves
// to admin, so disabling authentication does not enable authorization.
func AuthIdentityFrom(ctx context.Context) AuthIdentity {
	if identity, ok := ctx.Value(AuthContextKey{}).(AuthIdentity); ok {
		return identity
	}
	return AuthIdentity{Capabilities: mcptools.AdminMCPCapabilitySet()}
}

// AuditToolDecision logs every allow and deny with the resolved identity.
func AuditToolDecision(identity AuthIdentity, tool string, allowed bool) {
	decision := "deny"
	if allowed {
		decision = "allow"
	}
	log.Printf("erun-mcp authz: %s tool=%s tenant=%s user=%s capabilities=%v",
		decision, tool, orUnset(identity.Tenant), orUnset(identity.User), identity.Capabilities.Names())
}

func orUnset(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
