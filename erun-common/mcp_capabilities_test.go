package eruncommon

import (
	"slices"
	"testing"
)

// The table is the allowlist. A tool that writes anything is admin, and a tool
// nobody has classified is admin too — a new tool must be unreachable to a
// read-only caller until someone decides otherwise, not silently reachable.
func TestOnlyObservationIsReadCapability(t *testing.T) {
	for _, tool := range []string{
		"version", "list", "idle", "idle_stop_history", "context_list",
		"cloud_list", "diff", "outputs_list", "outputs_download",
		"job_status", "job_output", "job_await",
		"exec_job_status", "exec_job_output", "exec_job_await",
	} {
		if got := MCPToolCapability(tool); got != MCPCapabilityRead {
			t.Fatalf("%s should be readable, got %s", tool, got)
		}
	}

	// Remote execution, everything that mutates, and the leases that hold an
	// environment awake (and therefore spend money).
	for _, tool := range []string{
		"raw", "build", "push", "deploy", "delete", "init", "release", "upgrade",
		"expose", "terraform", "publish", "pin", "doctor", "contribute_clone",
		"context_init", "context_start", "context_stop",
		"cloud_login", "cloud_set", "cloud_init_aws", "cloud_inject_aws_credentials",
		"job_cancel", "job_attach", "exec_job_cancel", "exec_job_attach", "exec_agent",
		"activity_lease_take", "activity_lease_release",
		"idle_stop_cancel", "idle_stop_record",
	} {
		if got := MCPToolCapability(tool); got != MCPCapabilityAdmin {
			t.Fatalf("%s must require admin, got %s", tool, got)
		}
	}

	if got := MCPToolCapability("a_tool_added_next_week"); got != MCPCapabilityAdmin {
		t.Fatalf("an unclassified tool must fail closed, got %s", got)
	}
}

func TestAdminImpliesReadButReadDoesNotImplyAdmin(t *testing.T) {
	read := NewMCPCapabilitySet([]string{string(MCPCapabilityRead)})
	admin := NewMCPCapabilitySet([]string{string(MCPCapabilityAdmin)})

	if !read.AllowsTool("version") || read.AllowsTool("raw") {
		t.Fatalf("a read token may observe and may not execute: %+v", read)
	}
	if !admin.AllowsTool("version") || !admin.AllowsTool("raw") {
		t.Fatalf("an admin token may do everything: %+v", admin)
	}
}

// Adding this gate must not lock out a caller that worked yesterday: the
// desktop mints a token for a single operator who is the tenant admin, and
// those tokens say nothing about capabilities.
func TestATokenSayingNothingAboutCapabilitiesIsTheAdminDesktopCase(t *testing.T) {
	set := MCPCapabilitiesFromClaims("", nil)
	if !set.AllowsTool("raw") {
		t.Fatalf("a capability-less token is the coarse desktop admin, got %+v", set)
	}
}

// Both shapes issuers actually produce.
func TestCapabilitiesComeFromEitherScopeOrRoles(t *testing.T) {
	fromScope := MCPCapabilitiesFromClaims("openid erun:read", nil)
	if !fromScope.AllowsTool("list") || fromScope.AllowsTool("deploy") {
		t.Fatalf("a read scope grants read only, got %+v", fromScope)
	}

	fromRoles := MCPCapabilitiesFromClaims("", []string{"erun:admin"})
	if !fromRoles.AllowsTool("deploy") {
		t.Fatalf("an admin role grants admin, got %+v", fromRoles)
	}
}

// A token carrying roles, none of them erun's, must not be promoted to admin by
// the "said nothing" default — that would make an unrelated role a privilege
// escalation.
func TestUnrelatedRolesGrantNothingRatherThanEverything(t *testing.T) {
	set := MCPCapabilitiesFromClaims("openid profile", []string{"billing:reader"})
	if !set.Empty() {
		t.Fatalf("unrelated roles must grant nothing, got %+v", set)
	}
	if set.AllowsTool("version") || set.AllowsTool("raw") {
		t.Fatalf("an empty set permits nothing, got %+v", set)
	}
}

// This is the test that must fail loudly if a future tool is registered under
// the attach tier without being re-examined against this boundary: a caller
// scoped to attach only must never reach remote execution or anything that
// mutates an environment, regardless of what else the tool surface grows into.
func TestAttachCapabilityCannotReachExecutionOrMutation(t *testing.T) {
	attach := NewMCPCapabilitySet([]string{string(MCPCapabilityAttach)})

	if !attach.Allows(MCPCapabilityAttach) {
		t.Fatalf("an attach token must allow the attach capability itself: %+v", attach)
	}
	for _, forbidden := range []string{
		"exec_raw", "raw", "build", "push", "deploy", "delete", "release",
		"upgrade", "expose", "terraform", "init", "context_init",
	} {
		if attach.AllowsTool(forbidden) {
			t.Fatalf("an attach-only token must not reach %q: %+v", forbidden, attach)
		}
	}
}

// Attach is a distinct tier, not a wider read: it does not inherit read-only
// observation, and neither read nor admin is granted just because a caller
// resolves an attach token -- only admin reaches back down into attach.
func TestAttachDoesNotImplyReadAndReadDoesNotImplyAttach(t *testing.T) {
	attach := NewMCPCapabilitySet([]string{string(MCPCapabilityAttach)})
	if attach.AllowsTool("version") || attach.Allows(MCPCapabilityRead) {
		t.Fatalf("an attach-only token must not gain read observation: %+v", attach)
	}

	read := NewMCPCapabilitySet([]string{string(MCPCapabilityRead)})
	if read.Allows(MCPCapabilityAttach) {
		t.Fatalf("a read-only token must not gain attach: %+v", read)
	}

	admin := NewMCPCapabilitySet([]string{string(MCPCapabilityAdmin)})
	if !admin.Allows(MCPCapabilityAttach) {
		t.Fatalf("admin permits everything, including attach: %+v", admin)
	}
}

// Both shapes issuers actually produce, mirroring how erun:read/erun:admin
// already reach a token -- an issuer that wants to grant a mobile-scoped
// caller attach only says so explicitly in scope or roles.
func TestAttachCapabilityComesFromEitherScopeOrRoles(t *testing.T) {
	fromScope := MCPCapabilitiesFromClaims("openid erun:attach", nil)
	if !fromScope.Allows(MCPCapabilityAttach) || fromScope.Allows(MCPCapabilityRead) || fromScope.Allows(MCPCapabilityAdmin) {
		t.Fatalf("an attach scope grants attach only, got %+v", fromScope)
	}

	fromRoles := MCPCapabilitiesFromClaims("", []string{"erun:attach"})
	if !fromRoles.Allows(MCPCapabilityAttach) {
		t.Fatalf("an attach role grants attach, got %+v", fromRoles)
	}
}

// A role that merely resembles the attach tier's name must not resolve to it --
// the fail-closed-on-unrecognized-role behavior MCPCapabilitiesFromClaims
// already applies to erun:read/erun:admin must hold for the new tier too, or
// adding a third tier would have quietly widened what "unrecognised" means.
func TestANearMissRoleNeverResolvesToAttach(t *testing.T) {
	set := MCPCapabilitiesFromClaims("", []string{"erun:attaching", "mobile:attach"})
	if !set.Empty() {
		t.Fatalf("a role that merely resembles erun:attach must not resolve to it, got %+v", set)
	}
	if set.Allows(MCPCapabilityAttach) {
		t.Fatalf("a near-miss role string must not grant attach: %+v", set)
	}
}

// The key identifies one distinct tool surface, so servers can be cached by it.
func TestCapabilityKeyIdentifiesTheToolSurface(t *testing.T) {
	a := NewMCPCapabilitySet([]string{"erun:read"})
	b := NewMCPCapabilitySet([]string{"erun:read"})
	admin := NewMCPCapabilitySet([]string{"erun:admin"})
	attach := NewMCPCapabilitySet([]string{"erun:attach"})

	if a.Key() != b.Key() {
		t.Fatalf("equal capabilities must share a key: %q vs %q", a.Key(), b.Key())
	}
	if a.Key() == admin.Key() {
		t.Fatalf("different capabilities must not share a key: %q", a.Key())
	}
	if a.Key() == attach.Key() || admin.Key() == attach.Key() {
		t.Fatalf("attach must be its own distinct key: read=%q admin=%q attach=%q", a.Key(), admin.Key(), attach.Key())
	}
	if !slices.Equal(admin.Names(), []string{"erun:admin"}) {
		t.Fatalf("unexpected names: %v", admin.Names())
	}
	if !slices.Equal(attach.Names(), []string{"erun:attach"}) {
		t.Fatalf("unexpected names: %v", attach.Names())
	}
}
