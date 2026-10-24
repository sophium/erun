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

func testDeleteRealRunRetractsChallengesBeforeDeletingTheNamespace(t *testing.T) {
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
}

func testDeleteRealRunReportsARefusedChallengeReadInsteadOfSkippingSilently(t *testing.T) {
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
}

func testDeleteRealRunWithoutCertManagerSkipsTheRetraction(t *testing.T) {
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
}

func testDeleteRealRunNamespaceDeleteFailureWarnsAndContinues(t *testing.T) {
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
}

func testDeleteRealRunWithOutputJSONReportsNamespaceDeleteFailure(t *testing.T) {
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
}

func testDeleteRealRunRefusedNamespaceDeleteReportsTheRefusalNotATimeout(t *testing.T) {
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
}

func testDeleteRealRunWedgedNamespaceDeleteStillReportsTheTerminationTimeout(t *testing.T) {
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
}

func testDeleteRealRunNamespaceAlreadyGoneAfterAFailedDeleteReportsSuccess(t *testing.T) {
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
}
