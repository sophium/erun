package eruncommon

import (
	"sort"
	"strings"
)

// Authentication answers which tenant a caller is; this answers what that caller
// may do once it is in. The two are separate questions and the edge needs both:
// a token that proves a tenant still reaches `raw`, which can kubectl-exec, and
// `deploy`/`delete`/`context_*`, which mutate. A caller that only needs to watch
// an environment should not be one typo away from rebuilding it.
//
// The table lives here rather than in erun-mcp so the edge and any other
// transport that grows an authorization story read the same mapping. A tool
// appears in exactly one capability, and anything absent is admin — a new tool
// is dangerous until someone has decided otherwise, so the default fails closed.

type MCPCapability string

const (
	// MCPCapabilityRead permits observation that cannot change the environment.
	MCPCapabilityRead MCPCapability = "erun:read"
	// MCPCapabilityAdmin permits everything, including remote execution.
	MCPCapabilityAdmin MCPCapability = "erun:admin"
)

// mcpReadOnlyTools are the tools that only observe. Membership is the strict
// test — a tool that writes anything at all, including keeping an environment
// awake, is not here. Leases look harmless and are not: holding one defers
// auto-stop, which spends money.
var mcpReadOnlyTools = map[string]struct{}{
	"version":             {},
	"list":                {},
	"idle":                {},
	"activity_lease_list": {},
	"idle_stop_history":   {},
	"context_list":        {},
	"cloud_list":          {},
	"diff":                {},
	"outputs_list":        {},
	"outputs_download":    {},
	"job_status":          {},
	"job_output":          {},
	"job_await":           {},
}

// MCPToolCapability returns the capability a tool requires. An unknown tool
// requires admin: the table is the allowlist, so a tool added without a decision
// about its blast radius is unreachable to a read-only caller rather than
// silently reachable.
func MCPToolCapability(tool string) MCPCapability {
	if _, ok := mcpReadOnlyTools[strings.TrimSpace(tool)]; ok {
		return MCPCapabilityRead
	}
	return MCPCapabilityAdmin
}

// MCPCapabilitySet is a caller's resolved capabilities.
type MCPCapabilitySet struct {
	read  bool
	admin bool
}

// NewMCPCapabilitySet builds a set from resolved capability names. Unrecognised
// names are ignored rather than rejected, so an IdP that grants unrelated roles
// alongside erun's does not lock the caller out.
func NewMCPCapabilitySet(capabilities []string) MCPCapabilitySet {
	var set MCPCapabilitySet
	for _, capability := range capabilities {
		switch MCPCapability(strings.TrimSpace(capability)) {
		case MCPCapabilityAdmin:
			set.admin = true
		case MCPCapabilityRead:
			set.read = true
		}
	}
	return set
}

// AdminMCPCapabilitySet is the desktop's coarse case: a single operator who is
// the tenant admin. It is also the compatibility default for a token minted
// before capabilities existed, so adding this gate cannot lock out a caller that
// worked yesterday — narrowing is opt-in, per the same direction as the rest of
// the edge's auth.
func AdminMCPCapabilitySet() MCPCapabilitySet {
	return MCPCapabilitySet{read: true, admin: true}
}

// Allows reports whether the set satisfies a required capability. Admin implies
// read, so an admin token never has to carry both.
func (s MCPCapabilitySet) Allows(required MCPCapability) bool {
	switch required {
	case MCPCapabilityAdmin:
		return s.admin
	case MCPCapabilityRead:
		return s.read || s.admin
	}
	return false
}

// AllowsTool is the question the edge actually asks.
func (s MCPCapabilitySet) AllowsTool(tool string) bool {
	return s.Allows(MCPToolCapability(tool))
}

// Empty reports a set that permits nothing, which is what an authenticated token
// carrying only unrecognised roles resolves to.
func (s MCPCapabilitySet) Empty() bool { return !s.read && !s.admin }

// Names returns the granted capabilities, sorted, for audit lines and for the
// cache key that identifies one distinct tool set.
func (s MCPCapabilitySet) Names() []string {
	names := make([]string, 0, 2)
	if s.admin {
		names = append(names, string(MCPCapabilityAdmin))
	}
	if s.read {
		names = append(names, string(MCPCapabilityRead))
	}
	sort.Strings(names)
	return names
}

// Key identifies a capability set, so a server built for one set can be reused
// for every caller carrying the same one.
func (s MCPCapabilitySet) Key() string { return strings.Join(s.Names(), ",") }

// MCPCapabilitiesFromClaims resolves what a token grants. Two shapes are
// accepted because two issuers produce them: a space-delimited OAuth `scope`
// string, and a roles array of the kind Zitadel project roles arrive in.
//
// A token that carries neither is treated as admin. That is the desktop's
// existing single-operator model, and it is what keeps this change from
// breaking every token minted before capabilities existed; an issuer that wants
// a narrower caller says so explicitly.
func MCPCapabilitiesFromClaims(scope string, roles []string) MCPCapabilitySet {
	names := make([]string, 0, len(roles)+2)
	names = append(names, strings.Fields(scope)...)
	for _, role := range roles {
		names = append(names, strings.TrimSpace(role))
	}
	if len(names) == 0 {
		return AdminMCPCapabilitySet()
	}
	set := NewMCPCapabilitySet(names)
	if set.Empty() {
		// The token said something about roles, none of it erun's. Treating that
		// as admin would make an unrelated role a privilege escalation, so it
		// resolves to nothing and the caller sees an empty tool set.
		return MCPCapabilitySet{}
	}
	return set
}
