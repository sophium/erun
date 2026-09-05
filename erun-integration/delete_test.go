package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestDelete(t *testing.T) {
	t.Parallel()
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"delete", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_seeded_env", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "y\n",
		})
		golden.Equal(t, "delete/dry_run_with_seeded_env", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_runtime_type_traces_namespace_delete", func(t *testing.T) {
		// A runtime-type env counts as remote for delete: it must trace the
		// kubectl namespace delete like a remote=true env.
		setup := env.New(t)
		seedExplicitTypeEnv(t, setup, "team", "prod", "runtime")
		result := erun.Run(t, []string{"delete", "team", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/dry_run_with_runtime_type_traces_namespace_delete", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_remote_env_traces_namespace_delete", func(t *testing.T) {
		// A remote env's dry-run traces the kubectl namespace delete
		// alongside the local config removal, without touching the cluster.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/dry_run_with_remote_env_traces_namespace_delete", normalize.Apply(result.Combined))
	})

	t.Run("rejects_remote_env_without_kubernetes_context", func(t *testing.T) {
		// Regression: a remote env missing its kubernetescontext field used
		// to silently delete the namespace from the host's current kubectl
		// context (e.g. a developer's orbstack). Delete now errors up front.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		body := "name: dev\n" +
			"repopath: " + setup.Cwd + "\n" +
			"runtimeversion: 1.0.0\n" +
			"type: remote-agent\n"
		if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("rewrite env config without kubernetescontext: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		// kubectl stub still emits success if invoked; the test asserts
		// erun never gets there.
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when remote env has no kubernetescontext, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "kubernetes context is required") {
			t.Fatalf("expected RequireKubernetesContext error, got:\n%s", out)
		}
		// The local config tree must remain on disk: erun must not delete
		// local state when the remote-side delete cannot proceed safely.
		if _, err := os.Stat(filepath.Join(setup.ConfigHome, "erun", "team", "dev")); err != nil {
			t.Errorf("env config tree should remain on disk when delete aborts, stat err: %v", err)
		}
	})

	t.Run("real_run_confirmation_prompt_accepts_matching_input", func(t *testing.T) {
		// Happy path without --yes: the prompt requires the literal
		// "<tenant>-<environment>" string to proceed. Deleting the tenant's
		// last (local) env cascades to clearing the tenant config and the
		// root default tenant.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "team-dev\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_confirmation_prompt_accepts_matching_input", normalize.Apply(result.Combined))
		if _, err := os.Stat(envDir); !os.IsNotExist(err) {
			t.Errorf("expected env config tree to be removed at %s, stat err: %v", envDir, err)
		}
	})

	t.Run("real_run_confirmation_mismatch_aborts", func(t *testing.T) {
		// A mismatched confirmation (anything but "<tenant>-<environment>")
		// must abort before any state is touched — config tree stays intact.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "team-prod\n",
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on confirmation mismatch, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "delete/real_run_confirmation_mismatch_aborts", normalize.Apply(result.Combined))
		if _, err := os.Stat(envDir); err != nil {
			t.Errorf("env config tree must remain on disk after aborted delete, stat err: %v", err)
		}
	})

	t.Run("real_run_default_env_reassigned_when_other_envs_remain", func(t *testing.T) {
		// Deleting the tenant's default env while another env remains must
		// keep the tenant and promote the next env to default, not leave a
		// dangling default reference.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stagingDir := filepath.Join(setup.ConfigHome, "erun", "team", "staging")
		if err := os.MkdirAll(stagingDir, 0o755); err != nil {
			t.Fatalf("mkdir staging env: %v", err)
		}
		mustWrite(t, filepath.Join(stagingDir, "config.yaml"),
			"name: staging\n"+
				"repopath: "+setup.Cwd+"\n"+
				"kubernetescontext: test-context\n"+
				"containerregistry: registry.example/test\n"+
				"runtimeversion: 1.0.0\n",
		)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_default_env_reassigned_when_other_envs_remain", normalize.Apply(result.Combined))
		tenantCfg, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml"))
		if err != nil {
			t.Fatalf("read tenant config (tenant must survive while envs remain): %v", err)
		}
		if !strings.Contains(string(tenantCfg), "defaultenvironment: staging") {
			t.Errorf("expected default environment reassigned to staging, got:\n%s", tenantCfg)
		}
	})

	t.Run("real_run_last_env_of_non_default_tenant_keeps_root_default", func(t *testing.T) {
		// Removing the last env of a secondary (non-default) tenant deletes
		// that tenant's config but must leave the root default tenant
		// untouched.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 0)
		result := erun.Run(t, []string{"delete", "other", "staging", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_last_env_of_non_default_tenant_keeps_root_default", normalize.Apply(result.Combined))
		if _, err := os.Stat(filepath.Join(setup.ConfigHome, "erun", "other")); !os.IsNotExist(err) {
			t.Errorf("expected secondary tenant config tree to be removed, stat err: %v", err)
		}
		rootCfg, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		if !strings.Contains(string(rootCfg), "defaulttenant: team") {
			t.Errorf("root default tenant must survive deleting a non-default tenant, got:\n%s", rootCfg)
		}
	})

	t.Run("real_run_retracts_challenges_before_deleting_the_namespace", func(t *testing.T) {
		// #1174: namespace teardown removes the env's DNS-01 token Secret as
		// ordinary content, and Kubernetes gives no ordering guarantee against
		// cert-manager finalizing a still-pending Challenge in the same
		// namespace. The Challenge's cleanup needs that Secret to retract its
		// record, so if the namespace goes first the finalizer never clears and
		// the namespace sits in Terminating for the full 20-minute timeout.
		// The delete must therefore retract challenges FIRST, while the
		// credential still exists. This asserts that ordering directly.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		callLog := filepath.Join(setup.Cwd, "kubectl-calls.log")
		counter := filepath.Join(setup.Cwd, "challenge-gets")
		fixture.StubBinaryWithScript(t, stubs, "kubectl",
			`printf '%s\n' "$*" >> '`+filepath.ToSlash(callLog)+`'
case "$*" in
  *"get challenges"*)
    # Present on the first look, gone once the Certificate delete has been
    # processed -- the real asynchronous shape, so the wait loop is exercised
    # rather than short-circuited.
    if [ -f '`+filepath.ToSlash(counter)+`' ]; then
      exit 0
    fi
    printf '%s\n' 'challenge.acme.cert-manager.io/team-dev-wildcard-1-x'
    ;;
  *"delete certificates.cert-manager.io"*)
    : > '`+filepath.ToSlash(counter)+`'
    ;;
esac
exit 0
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}

		logged, err := os.ReadFile(callLog)
		if err != nil {
			t.Fatalf("read kubectl call log: %v", err)
		}
		calls := string(logged)
		certDelete := strings.Index(calls, "delete certificates.cert-manager.io")
		nsDelete := strings.Index(calls, "delete namespace team-dev")
		if certDelete < 0 {
			t.Fatalf("no certificate delete was issued; challenges would never be retracted:\n%s", calls)
		}
		if nsDelete < 0 {
			t.Fatalf("the namespace was never deleted:\n%s", calls)
		}
		if certDelete > nsDelete {
			t.Fatalf("certificates were deleted AFTER the namespace, which is the bug:\n%s", calls)
		}
		if !strings.Contains(calls, "get challenges.acme.cert-manager.io") {
			t.Fatalf("the retraction never waited for the challenges to finalize:\n%s", calls)
		}
	})

	t.Run("real_run_reports_a_refused_challenge_read_instead_of_skipping_silently", func(t *testing.T) {
		// #1183: the retraction shipped inert. Its challenge read was Forbidden
		// (the delete Job's ServiceAccount had no access to
		// acme.cert-manager.io), the code folded every error into "no
		// challenges here", and the whole step was skipped with nothing to say
		// so -- so the namespace wedged for its full timeout exactly as before.
		//
		// A cluster without cert-manager must still skip silently; that is the
		// next scenario. A cluster that HAS it and refuses the read must say so,
		// because the namespace is about to wedge and the reason is not in the
		// namespace's own conditions.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl",
			`case "$*" in
  *"get challenges"*)
    printf '%s\n' 'Error from server (Forbidden): challenges.acme.cert-manager.io is forbidden: User "system:serviceaccount:team-prod:team-env-deployer" cannot list resource "challenges"' >&2
    exit 1
    ;;
  *"delete namespace"*)
    # What a wedged namespace actually does: the delete waits out its timeout
    # and fails. That is the path the retraction note has to surface on.
    printf '%s\n' 'error: timed out waiting for the condition on namespaces/team-dev' >&2
    exit 1
    ;;
  *"get namespace"*)
    # Still present afterwards, so the blocked-delete branch runs rather than
    # the benign "it disappeared during the wait" one.
    printf '%s\n' 'namespace/team-dev'
    ;;
esac
exit 0
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}

		if !strings.Contains(result.Combined, "retraction could not run") {
			t.Fatalf("a refused challenge read must be reported, not swallowed; got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "forbidden") && !strings.Contains(result.Combined, "Forbidden") {
			t.Fatalf("the report must carry kubectl's own reason so the cause is actionable; got:\n%s", result.Combined)
		}
	})

	t.Run("real_run_without_cert_manager_skips_the_retraction", func(t *testing.T) {
		// The retraction is an optimization, so a cluster with no cert-manager
		// (the normal case for a local or test cluster, where the CRDs do not
		// exist and kubectl reports an unknown resource type) must pay nothing
		// for it: no certificate delete, no waiting, and the namespace delete
		// unaffected.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		callLog := filepath.Join(setup.Cwd, "kubectl-calls.log")
		fixture.StubBinaryWithScript(t, stubs, "kubectl",
			`printf '%s\n' "$*" >> '`+filepath.ToSlash(callLog)+`'
case "$*" in
  *"get challenges"*)
    printf '%s\n' 'error: the server doesn'"'"'t have a resource type "challenges"' >&2
    exit 1
    ;;
esac
exit 0
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}

		logged, err := os.ReadFile(callLog)
		if err != nil {
			t.Fatalf("read kubectl call log: %v", err)
		}
		calls := string(logged)
		if strings.Contains(calls, "delete certificates.cert-manager.io") {
			t.Fatalf("a cluster without cert-manager must not be asked to delete certificates:\n%s", calls)
		}
		if !strings.Contains(calls, "delete namespace team-dev") {
			t.Fatalf("the namespace delete must still happen:\n%s", calls)
		}
	})

	t.Run("real_run_namespace_delete_failure_warns_and_continues", func(t *testing.T) {
		// A failed namespace delete is non-fatal: kubectl's error is surfaced
		// as a warning on stderr and the local config delete still proceeds.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
			Stderr:   `Error from server (Forbidden): namespaces "team-dev" is forbidden`,
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_namespace_delete_failure_warns_and_continues", normalize.Apply(result.Combined))
		if _, err := os.Stat(envDir); !os.IsNotExist(err) {
			t.Errorf("local config delete must continue after namespace failure, stat err: %v", err)
		}
	})

	t.Run("real_run_with_output_json_reports_namespace_delete_failure", func(t *testing.T) {
		// The hosted delete Job (#1140) reads this exact shape back out of its
		// own captured output to tell a blocked namespace teardown apart from
		// a clean one, since the command's own exit code stays 0 either way.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
			Stderr:   `Error from server (Forbidden): namespaces "team-dev" is forbidden`,
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_with_output_json_reports_namespace_delete_failure", normalize.Apply(result.Combined))
	})

	t.Run("real_run_refused_namespace_delete_reports_the_refusal_not_a_timeout", func(t *testing.T) {
		// A delete the cluster refuses outright -- here RBAC, in milliseconds --
		// used to be reported as a twenty-minute termination timeout carrying the
		// namespace's termination conditions, sending an operator to investigate
		// finalizers when the real fault was a missing grant. It must instead
		// report kubectl's own refusal and name the permission problem. The
		// challenge read is refused by the same kubeconfig, which is what proves
		// the retraction note is left out too: it warns about holding up a delete
		// that here was never in flight.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl",
			`case "$*" in
  *"get challenges"*)
    printf '%s\n' 'Error from server (Forbidden): challenges.acme.cert-manager.io is forbidden: User "jane" cannot list resource "challenges"' >&2
    exit 1
    ;;
  *"delete namespace"*)
    printf '%s\n' 'Error from server (Forbidden): namespaces "team-dev" is forbidden: User "jane" cannot delete resource "namespaces" in API group "" at the cluster scope' >&2
    exit 1
    ;;
  *"get namespace"*)
    # The delete never took effect, so the namespace is still Active -- the
    # exact state that used to be misread as wedged in Terminating.
    printf '%s\n' 'namespace/team-dev'
    ;;
esac
exit 0
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_refused_namespace_delete_reports_the_refusal_not_a_timeout", normalize.Apply(result.Combined))
	})

	t.Run("real_run_wedged_namespace_delete_still_reports_the_termination_timeout", func(t *testing.T) {
		// The counterpart to the refusal scenario above, and the one that stops
		// the two cases being flattened into one: a delete the cluster accepted
		// and then could not finish must still read as a termination timeout,
		// carrying the namespace's own conditions and the retraction note in the
		// single string an operator reads off the environment row.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl",
			`case "$*" in
  *"get challenges"*)
    printf '%s\n' 'Error from server (Forbidden): challenges.acme.cert-manager.io is forbidden: User "jane" cannot list resource "challenges"' >&2
    exit 1
    ;;
  *"-o jsonpath"*)
    printf '%s\t%s\n' 'NamespaceContentRemaining=True' 'challenges.acme.cert-manager.io has 1 resource instances'
    printf '%s\t%s\n' 'NamespaceFinalizersRemaining=True' 'acme.cert-manager.io/finalizer in 1 resource instances'
    ;;
  *"delete namespace"*)
    printf '%s\n' 'error: timed out waiting for the condition on namespaces/team-dev' >&2
    exit 1
    ;;
  *"get namespace"*)
    printf '%s\n' 'namespace/team-dev'
    ;;
esac
exit 0
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_wedged_namespace_delete_still_reports_the_termination_timeout", normalize.Apply(result.Combined))
	})

	t.Run("real_run_namespace_already_gone_after_a_failed_delete_reports_success", func(t *testing.T) {
		// The benign race the delete is built to tolerate: kubectl's wait fails,
		// but by the time erun looks the namespace has actually gone. That is a
		// success, so it must report no warning at all -- neither a refusal nor a
		// termination timeout. (A delete that simply succeeds is covered by
		// real_run_with_yes_flag_skips_confirmation_and_removes_config below.)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl",
			`case "$*" in
  *"delete namespace"*)
    printf '%s\n' 'error: An error occurred while waiting for the object to be deleted: unable to decode an event from the watch stream' >&2
    exit 1
    ;;
  *"get namespace"*)
    printf '%s\n' 'Error from server (NotFound): namespaces "team-dev" not found' >&2
    exit 1
    ;;
esac
exit 0
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_namespace_already_gone_after_a_failed_delete_reports_success", normalize.Apply(result.Combined))
	})

	t.Run("real_run_with_yes_flag_skips_confirmation_and_removes_config", func(t *testing.T) {
		// --yes bypasses the confirmation prompt and really removes the env
		// config tree.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		if _, err := os.Stat(envDir); err != nil {
			t.Fatalf("seeded env config missing before delete: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Filesystem state — golden cannot assert this; keep the os.Stat
		// check.
		if _, err := os.Stat(envDir); !os.IsNotExist(err) {
			t.Errorf("expected env config tree to be removed at %s, stat err: %v", envDir, err)
		}
		golden.Equal(t, "delete/real_run_with_yes_flag_skips_confirmation_and_removes_config", normalize.Apply(result.Combined))
	})

	t.Run("real_run_removes_the_environments_port_forward_state", func(t *testing.T) {
		// A port-forward state file names a local port, and that port range is
		// freed and reissued to whichever environment is created next. Leaving
		// the file behind after delete lets it resolve to a live forward that
		// now belongs to somebody else, so delete must clear it the same way it
		// clears the rest of the environment's footprint.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		seedMCPPortForwardState(t, setup, "team", "dev", 26100)
		statePath := portForwardStateFile(setup, "mcp", "team", "dev")
		if _, err := os.Stat(statePath); err != nil {
			t.Fatalf("seeded port-forward state missing before delete: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)

		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Filesystem state — golden cannot assert this; keep the os.Stat check
		// (mirrors real_run_with_yes_flag_skips_confirmation_and_removes_config).
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Errorf("expected the port-forward state file to be removed at %s, stat err: %v", statePath, err)
		}
	})

	t.Run("dry_run_traces_the_port_forward_state_removal_without_deleting_it", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		seedMCPPortForwardState(t, setup, "team", "dev", 26100)
		statePath := portForwardStateFile(setup, "mcp", "team", "dev")

		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "y\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, statePath) {
			t.Fatalf("expected the dry-run plan to name the port-forward state file, got:\n%s", result.Combined)
		}
		if _, err := os.Stat(statePath); err != nil {
			t.Errorf("a dry run must not remove the port-forward state file, stat err: %v", err)
		}
	})
}
