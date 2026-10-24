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
	// Challenges must be retracted before the namespace delete removes the DNS-01
	// Secret their finalizer needs; otherwise the namespace wedges in Terminating.
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
	// A Forbidden challenge read must be reported, not treated as "no challenges":
	// the namespace is about to wedge for a reason its conditions won't show.
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
	// Without cert-manager CRDs the retraction must cost nothing.
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
	// The hosted delete Job parses this shape from output, since exit stays 0 either way.
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
	// An immediate RBAC refusal must not read as a termination timeout, and the
	// retraction note is omitted since no delete was in flight.
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
	// An accepted delete that cannot finish must still read as a termination timeout.
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
	// kubectl's wait fails but the namespace is gone: success, no warning.
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
