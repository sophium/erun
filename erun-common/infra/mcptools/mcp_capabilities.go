package mcptools

import (
	"sort"
	"strings"
)

// Authorization: what an authenticated caller may do. A tool appears in exactly one
// capability; anything unlisted is admin, so new tools fail closed.

type MCPCapability string

const (
	// MCPCapabilityRead permits observation that cannot change the environment.
	MCPCapabilityRead MCPCapability = "erun:read"
	// MCPCapabilityAdmin permits everything, including remote execution.
	MCPCapabilityAdmin MCPCapability = "erun:admin"
	// MCPCapabilityAttach permits only driving an existing attach session. It neither grants nor is
	// granted by read or admin.
	MCPCapabilityAttach MCPCapability = "erun:attach"
	// MCPCapabilityOperate permits lifecycle operations on an existing environment (deploy a published
	// version, start/stop, resize), not creating/deleting environments or running arbitrary code.
	// It neither grants nor is granted by read.
	MCPCapabilityOperate MCPCapability = "erun:operate"
)

// mcpReadOnlyTools only observe. Leases are excluded: holding one defers auto-stop, which costs money.
var mcpReadOnlyTools = map[string]struct{}{
	"version": {},
	"list":    {},
	"idle":    {},
	// environment never exposes doctor's mutating recovery flags.
	"environment":         {},
	"ai_sessions":         {},
	"activity_lease_list": {},
	"idle_stop_history":   {},
	"context_list":        {},
	"cloud_list":          {},
	"exec_diff":           {},
	"observe":             {},
	// services is read-only, unlike expose/unexpose.
	"services":          {},
	"usage":             {},
	"outputs_list":      {},
	"outputs_download":  {},
	"exec_job_status":   {},
	"exec_job_output":   {},
	"exec_job_await":    {},
	"jobs_list":         {},
	"jobs_show":         {},
	"review_list":       {},
	"review_show":       {},
	"review_queue_list": {},
}

// mcpOperateTools excludes: push/build (publish to the shared registry), doctor (can force helm
// recovery), expose/unexpose (change external reachability), pin (rewrites repo versions), and
// context_init (creates a context).
var mcpOperateTools = map[string]struct{}{
	"deploy":        {},
	"context_start": {},
	"context_stop":  {},
	"resize":        {},
}

// MCPToolCapability returns the capability a tool requires; unknown tools require admin.
func MCPToolCapability(tool string) MCPCapability {
	// Resolve retired names first, or a rename would promote a read-only tool to admin.
	current := MCPToolCurrentName(tool)
	if _, ok := mcpReadOnlyTools[current]; ok {
		return MCPCapabilityRead
	}
	if _, ok := mcpOperateTools[current]; ok {
		return MCPCapabilityOperate
	}
	return MCPCapabilityAdmin
}

// knownMCPCapabilities validates minting requests, which must refuse unknown tiers, unlike claim
// resolution, which ignores them.
var knownMCPCapabilities = map[MCPCapability]struct{}{
	MCPCapabilityRead:    {},
	MCPCapabilityAdmin:   {},
	MCPCapabilityAttach:  {},
	MCPCapabilityOperate: {},
}

// IsKnownMCPCapability reports whether name is a defined capability tier.
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

// NewMCPCapabilitySet ignores unrecognised names so unrelated IdP roles do not lock a caller out.
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

// AdminMCPCapabilitySet is the desktop's single-operator case and the default for tokens without capabilities.
func AdminMCPCapabilitySet() MCPCapabilitySet {
	return MCPCapabilitySet{read: true, admin: true, attach: true, operate: true}
}

// Allows reports whether the set satisfies required; admin implies every capability.
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

// AllowsTool reports whether the set permits tool.
func (s MCPCapabilitySet) AllowsTool(tool string) bool {
	return s.Allows(MCPToolCapability(tool))
}

// Empty reports a set that permits nothing.
func (s MCPCapabilitySet) Empty() bool { return !s.read && !s.admin && !s.attach && !s.operate }

// Names returns the granted capabilities, sorted.
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

// Key identifies a capability set so servers can be shared across callers with the same one.
func (s MCPCapabilitySet) Key() string { return strings.Join(s.Names(), ",") }

// MCPCapabilitiesFromClaims resolves a token's grants from an OAuth scope string or a roles array.
// A token with neither is admin, so tokens minted before capabilities keep working.
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
		// Treating unrelated roles as admin would be a privilege escalation.
		return MCPCapabilitySet{}
	}
	return set
}
