package mcptools

import (
	"slices"
	"testing"
)

func TestOnlyObservationIsReadCapability(t *testing.T) {
	for _, tool := range []string{
		"version", "list", "idle", "idle_stop_history", "context_list",
		"cloud_list", "diff", "outputs_list", "outputs_download",
		"job_status", "job_output", "job_await",
		"exec_job_status", "exec_job_output", "exec_job_await",
		"environment", "ai_sessions",
	} {
		if got := MCPToolCapability(tool); got != MCPCapabilityRead {
			t.Fatalf("%s should be readable, got %s", tool, got)
		}
	}

	// deploy/context_start/context_stop/resize require erun:operate instead.
	for _, tool := range []string{
		"raw", "build", "push", "delete", "init", "release", "upgrade",
		"expose", "terraform", "publish", "pin", "doctor", "contribute_clone",
		"context_init",
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

func TestOperateCapabilityCoversAnExistingEnvironmentsLifecycle(t *testing.T) {
	for _, tool := range []string{"deploy", "context_start", "context_stop", "resize"} {
		if got := MCPToolCapability(tool); got != MCPCapabilityOperate {
			t.Fatalf("%s should require operate, got %s", tool, got)
		}
	}

	operate := NewMCPCapabilitySet([]string{string(MCPCapabilityOperate)})
	for _, tool := range []string{"deploy", "context_start", "context_stop", "resize"} {
		if !operate.AllowsTool(tool) {
			t.Fatalf("an operate token must reach %q: %+v", tool, operate)
		}
	}
	for _, forbidden := range []string{
		"exec_raw", "raw", "delete", "terraform", "init", "context_init",
		"build", "push", "doctor", "expose", "unexpose", "pin", "version",
	} {
		if operate.AllowsTool(forbidden) {
			t.Fatalf("an operate-only token must not reach %q: %+v", forbidden, operate)
		}
	}
}

func TestOperateDoesNotImplyReadAndReadDoesNotImplyOperate(t *testing.T) {
	operate := NewMCPCapabilitySet([]string{string(MCPCapabilityOperate)})
	if operate.Allows(MCPCapabilityRead) || operate.AllowsTool("version") {
		t.Fatalf("an operate-only token must not gain read observation: %+v", operate)
	}

	read := NewMCPCapabilitySet([]string{string(MCPCapabilityRead)})
	if read.Allows(MCPCapabilityOperate) || read.AllowsTool("deploy") {
		t.Fatalf("a read-only token must not gain operate: %+v", read)
	}

	admin := NewMCPCapabilitySet([]string{string(MCPCapabilityAdmin)})
	if !admin.Allows(MCPCapabilityOperate) || !admin.AllowsTool("deploy") {
		t.Fatalf("admin permits everything, including operate: %+v", admin)
	}
}

func TestOperateCapabilityComesFromEitherScopeOrRoles(t *testing.T) {
	fromScope := MCPCapabilitiesFromClaims("openid erun:operate", nil)
	if !fromScope.Allows(MCPCapabilityOperate) || fromScope.Allows(MCPCapabilityRead) || fromScope.Allows(MCPCapabilityAdmin) {
		t.Fatalf("an operate scope grants operate only, got %+v", fromScope)
	}

	fromRoles := MCPCapabilitiesFromClaims("", []string{"erun:operate"})
	if !fromRoles.Allows(MCPCapabilityOperate) {
		t.Fatalf("an operate role grants operate, got %+v", fromRoles)
	}
}

func TestANearMissRoleNeverResolvesToOperate(t *testing.T) {
	set := MCPCapabilitiesFromClaims("", []string{"erun:operating", "mobile:operate"})
	if !set.Empty() {
		t.Fatalf("a role that merely resembles erun:operate must not resolve to it, got %+v", set)
	}
	if set.Allows(MCPCapabilityOperate) {
		t.Fatalf("a near-miss role string must not grant operate: %+v", set)
	}
}

func TestAddingOperateDoesNotChangeWhatAdminCanDo(t *testing.T) {
	admin := AdminMCPCapabilitySet()
	for _, tool := range []string{
		"version", "list", "raw", "exec_raw", "build", "push", "deploy",
		"delete", "init", "release", "terraform", "context_init",
		"context_start", "context_stop", "resize", "doctor", "expose",
	} {
		if !admin.AllowsTool(tool) {
			t.Fatalf("admin must still reach %q, got %+v", tool, admin)
		}
	}
	if !slices.Equal(admin.Names(), []string{"erun:admin", "erun:attach", "erun:operate", "erun:read"}) {
		t.Fatalf("the admin desktop compatibility default must carry every tier explicitly, got %v", admin.Names())
	}
	if !admin.Allows(MCPCapabilityRead) || !admin.Allows(MCPCapabilityAttach) || !admin.Allows(MCPCapabilityOperate) || !admin.Allows(MCPCapabilityAdmin) {
		t.Fatalf("admin must satisfy every tier: %+v", admin)
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

func TestATokenSayingNothingAboutCapabilitiesIsTheAdminDesktopCase(t *testing.T) {
	set := MCPCapabilitiesFromClaims("", nil)
	if !set.AllowsTool("raw") {
		t.Fatalf("a capability-less token is the coarse desktop admin, got %+v", set)
	}
}

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

func TestUnrelatedRolesGrantNothingRatherThanEverything(t *testing.T) {
	set := MCPCapabilitiesFromClaims("openid profile", []string{"billing:reader"})
	if !set.Empty() {
		t.Fatalf("unrelated roles must grant nothing, got %+v", set)
	}
	if set.AllowsTool("version") || set.AllowsTool("raw") {
		t.Fatalf("an empty set permits nothing, got %+v", set)
	}
}

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

func TestANearMissRoleNeverResolvesToAttach(t *testing.T) {
	set := MCPCapabilitiesFromClaims("", []string{"erun:attaching", "mobile:attach"})
	if !set.Empty() {
		t.Fatalf("a role that merely resembles erun:attach must not resolve to it, got %+v", set)
	}
	if set.Allows(MCPCapabilityAttach) {
		t.Fatalf("a near-miss role string must not grant attach: %+v", set)
	}
}

func TestCapabilityKeyIdentifiesTheToolSurface(t *testing.T) {
	a := NewMCPCapabilitySet([]string{"erun:read"})
	b := NewMCPCapabilitySet([]string{"erun:read"})
	if a.Key() != b.Key() {
		t.Fatalf("equal capabilities must share a key: %q vs %q", a.Key(), b.Key())
	}

	tiers := []MCPCapability{MCPCapabilityRead, MCPCapabilityAdmin, MCPCapabilityAttach, MCPCapabilityOperate}
	sets := make(map[MCPCapability]MCPCapabilitySet, len(tiers))
	for _, tier := range tiers {
		sets[tier] = NewMCPCapabilitySet([]string{string(tier)})
		if !slices.Equal(sets[tier].Names(), []string{string(tier)}) {
			t.Fatalf("%s: unexpected names: %v", tier, sets[tier].Names())
		}
	}
	for i, left := range tiers {
		for _, right := range tiers[i+1:] {
			if sets[left].Key() == sets[right].Key() {
				t.Fatalf("%s and %s must not share a key: %q", left, right, sets[left].Key())
			}
		}
	}
}
