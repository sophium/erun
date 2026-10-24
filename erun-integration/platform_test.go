package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestPlatform(t *testing.T) {
	t.Parallel()
	t.Run("help", testPlatformHelp)
	t.Run("whoami_no_alias_configured", testPlatformWhoamiNoAliasConfigured)
	t.Run("whoami_dry_run_traces_resolved_call", testPlatformWhoamiDryRunTracesResolvedCall)
	t.Run("whoami_real_run", testPlatformWhoamiRealRun)
	t.Run("version_dry_run_traces_resolved_call", testPlatformVersionDryRunTracesResolvedCall)
	t.Run("version_real_run_without_a_working_token", testPlatformVersionRealRunWithoutAWorkingToken)
	t.Run("tenant_create_dry_run", testPlatformTenantCreateDryRun)
	t.Run("tenant_create_and_list_real_run", testPlatformTenantCreateAndListRealRun)
	t.Run("identity_org_create_dry_run", testPlatformIdentityOrgCreateDryRun)
	t.Run("identity_org_create_and_tenant_create_reachable_path_real_run", testPlatformIdentityOrgCreateAndTenantCreateReachablePathRealRun)
	t.Run("tenant_repair_org_mapping_dry_run", testPlatformTenantRepairOrgMappingDryRun)
	t.Run("tenant_repair_org_mapping_real_run", testPlatformTenantRepairOrgMappingRealRun)
	t.Run("user_grant_role_dry_run", testPlatformUserGrantRoleDryRun)
	t.Run("user_enroll_dry_run", testPlatformUserEnrollDryRun)
	t.Run("user_enroll_with_roles_dry_run", testPlatformUserEnrollWithRolesDryRun)
	t.Run("user_enroll_and_list_real_run", testPlatformUserEnrollAndListRealRun)
	t.Run("env_register_dry_run", testPlatformEnvRegisterDryRun)
	t.Run("env_lifecycle_real_run", testPlatformEnvLifecycleRealRun)
	t.Run("env_push_dry_run", testPlatformEnvPushDryRun)
	t.Run("env_pull_dry_run", testPlatformEnvPullDryRun)
	t.Run("env_definition_round_trip_real_run", testPlatformEnvDefinitionRoundTripRealRun)
	t.Run("env_pull_refuses_a_platform_name_mismatch", testPlatformEnvPullRefusesAPlatformNameMismatch)
	t.Run("env_pull_refuses_a_marker_pointing_at_another_tenant", testPlatformEnvPullRefusesAMarkerPointingAtAnotherTenant)
	t.Run("env_get_not_found", testPlatformEnvGetNotFound)
	t.Run("env_delete_dry_run_skips_confirmation", testPlatformEnvDeleteDryRunSkipsConfirmation)
	t.Run("context_create_preview_real_run", testPlatformContextCreatePreviewRealRun)
	t.Run("context_list_and_get_real_run", testPlatformContextListAndGetRealRun)
	t.Run("provision_dry_run", testPlatformProvisionDryRun)
	t.Run("provision_with_context_bootstrap_real_run", testPlatformProvisionWithContextBootstrapRealRun)
	t.Run("output_json", testPlatformOutputJSON)
}

func testPlatformHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"platform", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/help", normalize.Apply(result.Combined))
}

func testPlatformWhoamiNoAliasConfigured(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no erun alias configured, got:\n%s", result.Combined)
	}
	golden.Equal(t, "platform/whoami_no_alias_configured", normalize.Apply(result.Combined))
}

func testPlatformWhoamiDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"platform", "whoami", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/whoami_dry_run", normalize.Apply(result.Combined))
}

func testPlatformWhoamiRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "test-user") || !strings.Contains(result.Combined, "tenant-1") {
		t.Fatalf("expected whoami output to name the resolved identity, got:\n%s", result.Combined)
	}
	// whoami must print the tenant's real name ("acme", matching
	// GET /v1/tenants' name for the same tenant-1 id above), not just the
	// username ("test-user") -- the two must never be the only name shown.
	if !strings.Contains(result.Combined, "acme") {
		t.Fatalf("expected whoami output to name the tenant (matching tenant list), got:\n%s", result.Combined)
	}
}

func testPlatformVersionDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"platform", "version", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/version_dry_run", normalize.Apply(result.Combined))
}

func testPlatformVersionRealRunWithoutAWorkingToken(t *testing.T) {
	// GET /v1/platform is unauthenticated (see platformAPIStubServer's own
	// comment), so this must succeed even though the alias's cached access
	// token is garbage -- the whole point of exposing this route is that it
	// answers when a caller's credentials cannot be trusted to resolve
	// anything else.
	setup := env.New(t)
	server := platformAPIStubServer(t)
	alias := "erun+test@erun"
	seedERunCloudProviderAlias(t, setup, alias, server.URL, "cli-test-client")
	seedCachedERunAccessToken(t, setup, alias, "expired-or-garbage-token")
	result := erun.Run(t, []string{"platform", "version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "1.2.3") || !strings.Contains(result.Combined, "https://api.example.test") {
		t.Fatalf("expected the reported version and api url, got:\n%s", result.Combined)
	}
}

func testPlatformTenantCreateDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"platform", "tenant", "create", "--name", "acme", "--issuer", "https://idp.example", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/tenant_create_dry_run", normalize.Apply(result.Combined))
}

func testPlatformTenantCreateAndListRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	createResult := erun.Run(t, []string{"platform", "tenant", "create", "--name", "acme", "--issuer", "https://idp.example"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if createResult.ExitCode != 0 {
		t.Fatalf("create exit %d: %s", createResult.ExitCode, createResult.Combined)
	}
	if !strings.Contains(createResult.Combined, "acme") {
		t.Fatalf("expected created tenant to be named, got:\n%s", createResult.Combined)
	}
	listResult := erun.Run(t, []string{"platform", "tenant", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if listResult.ExitCode != 0 {
		t.Fatalf("list exit %d: %s", listResult.ExitCode, listResult.Combined)
	}
	if !strings.Contains(listResult.Combined, "acme") {
		t.Fatalf("expected tenant list to include acme, got:\n%s", listResult.Combined)
	}
	// A tenant no token can resolve to must be flagged where it is
	// listed, and a healthy one must not be.
	unreachableLine := ""
	acmeLine := ""
	for _, line := range strings.Split(listResult.Combined, "\n") {
		if strings.Contains(line, "probeco") {
			unreachableLine = line
		}
		if strings.Contains(line, "acme") {
			acmeLine = line
		}
	}
	if !strings.Contains(unreachableLine, "UNREACHABLE") {
		t.Fatalf("expected the unresolvable tenant to be flagged, got:\n%s", listResult.Combined)
	}
	if strings.Contains(acmeLine, "UNREACHABLE") {
		t.Fatalf("a resolvable tenant must not be flagged unreachable, got:\n%s", acmeLine)
	}
}

func testPlatformIdentityOrgCreateDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"platform", "identity", "org", "create", "--name", "acme", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/identity_org_create_dry_run", normalize.Apply(result.Combined))
}

// Without a way to obtain an org id, an org-scoped tenant could only be
// created with an empty, permanently unresolvable org value (see
// tenant_create_and_list_real_run's "probeco" fixture above). This proves
// the reachable path: create the org, feed its id into tenant create, and
// the same org-scoped issuer that refuses an empty value accepts this one.
func testPlatformIdentityOrgCreateAndTenantCreateReachablePathRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)

	orgResult := erun.Run(t, []string{"platform", "identity", "org", "create", "--name", "acme-reachable", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if orgResult.ExitCode != 0 {
		t.Fatalf("org create exit %d: %s", orgResult.ExitCode, orgResult.Combined)
	}
	var org struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(orgResult.Stdout), &org); err != nil || org.ID == "" {
		t.Fatalf("expected a JSON org result with an id, got err=%v combined:\n%s", err, orgResult.Combined)
	}

	const issuer = "https://auth.reachable-path.example"
	const orgFieldKey = "urn:zitadel:iam:user:resourceowner:id"

	// Without the org id this reachable path just produced, the same
	// issuer refuses -- proving the empty-org refusal is still live
	// and that what follows is a genuine positive case, not a stub that
	// always says yes.
	refused := erun.Run(t, []string{
		"platform", "tenant", "create", "--name", "acme-reachable-dead",
		"--issuer", issuer, "--org-field-key", orgFieldKey,
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if refused.ExitCode == 0 {
		t.Fatalf("expected an org-scoped tenant with no org value to be refused, got:\n%s", refused.Combined)
	}
	if !strings.Contains(refused.Combined, "erun platform identity org create") {
		t.Fatalf("refusal must name the org-create command, got:\n%s", refused.Combined)
	}

	resolvedResult := erun.Run(t, []string{
		"platform", "tenant", "create", "--name", "acme-reachable",
		"--issuer", issuer, "--org-field-key", orgFieldKey, "--org-field-value", org.ID,
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if resolvedResult.ExitCode != 0 {
		t.Fatalf("tenant create with the org id from the reachable path: exit %d: %s", resolvedResult.ExitCode, resolvedResult.Combined)
	}
	if !strings.Contains(resolvedResult.Combined, "acme-reachable") {
		t.Fatalf("expected the created tenant to be named, got:\n%s", resolvedResult.Combined)
	}
}

func testPlatformTenantRepairOrgMappingDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"platform", "tenant", "repair-org-mapping", "--tenant-id", "tenant-probeco",
		"--issuer", "https://idp.example", "--org-field-key", "urn:zitadel:iam:user:resourceowner:id",
		"--org-field-value", "42", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/tenant_repair_org_mapping_dry_run", normalize.Apply(result.Combined))
}

// The repair path for a tenant already stuck in probeco's state (created
// before the empty-org refusal, or left behind when its issuer was
// converted to org-scoped afterward): an operations caller repairs a
// tenant other than its own by naming --tenant-id explicitly.
func testPlatformTenantRepairOrgMappingRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{
		"platform", "tenant", "repair-org-mapping", "--tenant-id", "tenant-probeco",
		"--issuer", "https://auth.repair-path.example", "--org-field-key", "urn:zitadel:iam:user:resourceowner:id",
		"--org-field-value", "org-probeco-repaired",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "org-probeco-repaired") {
		t.Fatalf("expected the repaired org value to be reported, got:\n%s", result.Combined)
	}
}

// The post-enrollment grant: the one an operator reaches for after a user
// already exists, since re-enrolling an enrolled identity is a no-op that
// leaves its roles untouched. Both ids have to reach the request.
func testPlatformUserGrantRoleDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"platform", "user", "grant-role",
		"--user-id", "user-1", "--role-id", "role-reviewer", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/user_grant_role_dry_run", normalize.Apply(result.Combined))
}

func testPlatformUserEnrollDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"platform", "user", "enroll", "--username", "jane", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/user_enroll_dry_run", normalize.Apply(result.Combined))
}

// The cross-tenant recovery shape: an operations caller enrolls another
// tenant's administrator directly. Both flags have to reach the request,
// since an enrollment that lands as an ordinary member can only be elevated
// from inside a tenant that may have nobody able to do it.
func testPlatformUserEnrollWithRolesDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"platform", "user", "enroll", "--username", "jane",
		"--tenant-id", "tenant-b", "--role-id", "role-admin", "--role-id", "role-reader",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/user_enroll_with_roles_dry_run", normalize.Apply(result.Combined))
}

func testPlatformUserEnrollAndListRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	enrollResult := erun.Run(t, []string{"platform", "user", "enroll", "--username", "jane"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if enrollResult.ExitCode != 0 {
		t.Fatalf("enroll exit %d: %s", enrollResult.ExitCode, enrollResult.Combined)
	}
	listResult := erun.Run(t, []string{"platform", "user", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if listResult.ExitCode != 0 {
		t.Fatalf("list exit %d: %s", listResult.ExitCode, listResult.Combined)
	}
	if !strings.Contains(listResult.Combined, "test-user") {
		t.Fatalf("expected user list to include test-user, got:\n%s", listResult.Combined)
	}
}

func testPlatformContextCreatePreviewRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	args := []string{"platform", "context", "create", "--name", "prod", "--alias", "aws-main", "--region", "eu-west-2", "--preview"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "bootstrap cluster prod") {
		t.Fatalf("expected the preview plan in output, got:\n%s", result.Combined)
	}
}

func testPlatformContextListAndGetRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	list := erun.Run(t, []string{"platform", "context", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if list.ExitCode != 0 || !strings.Contains(list.Combined, "prod-cluster") {
		t.Fatalf("list exit %d: %s", list.ExitCode, list.Combined)
	}
	get := erun.Run(t, []string{"platform", "context", "get", "ctx-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if get.ExitCode != 0 || !strings.Contains(get.Combined, "ctx-1") {
		t.Fatalf("get exit %d: %s", get.ExitCode, get.Combined)
	}
}

func testPlatformProvisionDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"platform", "provision", "--env-name", "prod", "--env-type", "runtime", "--kubernetes-context", "prod-cluster", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/provision_dry_run", normalize.Apply(result.Combined))
}

func testPlatformProvisionWithContextBootstrapRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	args := []string{
		"platform", "provision", "--env-name", "prod", "--env-type", "runtime",
		"--context-name", "prod", "--context-alias", "aws-main", "--context-region", "eu-west-2",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "quota ok: true") {
		t.Fatalf("expected the resolved plan and quota line, got:\n%s", result.Combined)
	}
}

func testPlatformOutputJSON(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"platform", "whoami", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, `"tenantId": "tenant-1"`) {
		t.Fatalf("expected structured JSON result on stdout, got:\n%s", result.Combined)
	}
}
