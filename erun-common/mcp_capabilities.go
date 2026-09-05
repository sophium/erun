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
	// MCPCapabilityAttach permits driving an existing environment's attach
	// session (the dtach takeover protocol) and nothing else. It grants
	// neither read nor admin, and neither of those grants it back -- a caller
	// scoped to this tier only for a mobile client to reconnect to a running
	// session without also carrying `exec_raw`/`build`/`deploy`/`delete`.
	MCPCapabilityAttach MCPCapability = "erun:attach"
	// MCPCapabilityOperate permits driving the lifecycle of an environment
	// that already exists -- deploying an already-published version into it,
	// starting or stopping its cloud context, resizing its runtime pod --
	// without granting anything that decides what environments exist
	// (`init`/`delete`/`context_init`/`terraform`) or that runs arbitrary code
	// in one (`exec_raw` and the rest of the `exec_*` family, `build`/`push`,
	// which drive an arbitrary Dockerfile and publish to the registry). This
	// is the tier a loseable, sometimes-unattended, screenshot/clipboard-
	// exposed mobile client needs for erun#1107's Phase 3 -- operating an
	// environment -- without also being minted the same `erun:admin` a
	// developer's own attended machine holds. It grants neither read nor
	// admin, and neither of those grants it back, the same isolation
	// `erun:attach` already established.
	MCPCapabilityOperate MCPCapability = "erun:operate"
)

// mcpReadOnlyTools are the tools that only observe. Membership is the strict
// test — a tool that writes anything at all, including keeping an environment
// awake, is not here. Leases look harmless and are not: holding one defers
// auto-stop, which spends money.
var mcpReadOnlyTools = map[string]struct{}{
	"version": {},
	"list":    {},
	"idle":    {},
	// environment composes list/idle/doctor's own resolvers and never exposes
	// doctor's mutating recovery flags (clearPendingHelm/rollback) — the
	// authenticated-edge read model erun#1105 needs a scoped mobile caller to
	// reach.
	"environment": {},
	// ai_sessions only reads what "erun activity ai-session report" already
	// wrote; it has no write path of its own.
	"ai_sessions":         {},
	"activity_lease_list": {},
	"idle_stop_history":   {},
	"context_list":        {},
	"cloud_list":          {},
	"exec_diff":           {},
	"observe":             {},
	"usage":               {},
	"outputs_list":        {},
	"outputs_download":    {},
	"exec_job_status":     {},
	"exec_job_output":     {},
	"exec_job_await":      {},
	"review_list":         {},
	"review_show":         {},
	"review_queue_list":   {},
}

// mcpOperateTools are the tools that drive an existing environment's own
// lifecycle without deciding what environments exist or running arbitrary
// code in one. Membership is deliberately narrow -- see
// MCPCapabilityOperate's doc comment for the boundary, and below for the
// calls that were hard to place:
//
//   - `push`/`build` are excluded even though they are pure primitives too:
//     they publish to the shared image registry rather than acting on this
//     one environment, and Phase 3's mobile caller only ever deploys an
//     already-published version, never builds or publishes one.
//   - `doctor` is excluded: its recovery path can force a helm
//     pending-upgrade/pending-rollback (see mcpReadOnlyTools' comment on
//     `environment` above), which is a deeper intervention than routine
//     operation.
//   - `expose`/`unexpose` are excluded: publishing a service through the
//     platform edge changes what is reachable from outside the environment,
//     not just what runs inside it.
//   - `pin` is excluded: it rewrites version references across the repo
//     rather than acting on the running environment.
//   - `context_init` is excluded (`context_start`/`context_stop` are not):
//     it bootstraps a new cloud context, which is closer to deciding what
//     exists than to operating something that already does.
var mcpOperateTools = map[string]struct{}{
	"deploy":        {},
	"context_start": {},
	"context_stop":  {},
	"resize":        {},
}

// MCPToolCapability returns the capability a tool requires. An unknown tool
// requires admin: the table is the allowlist, so a tool added without a decision
// about its blast radius is unreachable to a read-only caller rather than
// silently reachable.
func MCPToolCapability(tool string) MCPCapability {
	// Resolve a retired name first, so a caller still using `diff` authorizes
	// exactly as one using `exec_diff` -- otherwise the rename would silently
	// promote a read-only tool to requiring admin (#1186).
	current := MCPToolCurrentName(tool)
	if _, ok := mcpReadOnlyTools[current]; ok {
		return MCPCapabilityRead
	}
	if _, ok := mcpOperateTools[current]; ok {
		return MCPCapabilityOperate
	}
	return MCPCapabilityAdmin
}

// knownMCPCapabilities are the tiers a caller may request when a route mints a
// token, as opposed to the tiers MCPCapabilitiesFromClaims resolves off a
// token already issued. The two must not share a validation strategy: resolving
// an already-trusted token's claims ignores anything it doesn't recognize (so
// an unrelated IdP role can't lock a caller out), but minting must refuse an
// unrecognized request outright -- the caller asking for a capability is not
// the authority on whether it exists.
var knownMCPCapabilities = map[MCPCapability]struct{}{
	MCPCapabilityRead:    {},
	MCPCapabilityAdmin:   {},
	MCPCapabilityAttach:  {},
	MCPCapabilityOperate: {},
}

// IsKnownMCPCapability reports whether name is one of the defined capability
// tiers. A minting path uses this to reject a requested scope outright rather
// than silently drop or widen it.
func IsKnownMCPCapability(name string) bool {
	_, ok := knownMCPCapabilities[MCPCapability(strings.TrimSpace(name))]
	return ok
}

// MCPCapabilitySet is a caller's resolved capabilities.
type MCPCapabilitySet struct {
	read    bool
	admin   bool
	attach  bool
	operate bool
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
		case MCPCapabilityAttach:
			set.attach = true
		case MCPCapabilityOperate:
			set.operate = true
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
	return MCPCapabilitySet{read: true, admin: true, attach: true, operate: true}
}

// Allows reports whether the set satisfies a required capability. Admin implies
// every other capability, so an admin token never has to carry them explicitly.
func (s MCPCapabilitySet) Allows(required MCPCapability) bool {
	switch required {
	case MCPCapabilityAdmin:
		return s.admin
	case MCPCapabilityRead:
		return s.read || s.admin
	case MCPCapabilityAttach:
		return s.attach || s.admin
	case MCPCapabilityOperate:
		return s.operate || s.admin
	}
	return false
}

// AllowsTool is the question the edge actually asks.
func (s MCPCapabilitySet) AllowsTool(tool string) bool {
	return s.Allows(MCPToolCapability(tool))
}

// Empty reports a set that permits nothing, which is what an authenticated token
// carrying only unrecognised roles resolves to.
func (s MCPCapabilitySet) Empty() bool { return !s.read && !s.admin && !s.attach && !s.operate }

// Names returns the granted capabilities, sorted, for audit lines and for the
// cache key that identifies one distinct tool set.
func (s MCPCapabilitySet) Names() []string {
	names := make([]string, 0, 4)
	if s.admin {
		names = append(names, string(MCPCapabilityAdmin))
	}
	if s.attach {
		names = append(names, string(MCPCapabilityAttach))
	}
	if s.operate {
		names = append(names, string(MCPCapabilityOperate))
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
