package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorCredentialScenarios(t *testing.T) {
	t.Run("dry_run_aws_alias_env_plans_host_credential_check", testDoctorDryRunAwsAliasEnvPlansHostCredentialCheck)
	t.Run("dry_run_remote_agent_env_plans_git_push_access_check", testDoctorDryRunRemoteAgentEnvPlansGitPushAccessCheck)
	t.Run("real_run_reports_fetch_works_push_credential_missing", testDoctorRealRunReportsFetchWorksPushCredentialMissing)
	t.Run("real_run_reports_git_push_credential_configured", testDoctorRealRunReportsGitPushCredentialConfigured)
	t.Run("real_run_reports_fetch_also_fails", testDoctorRealRunReportsFetchAlsoFails)
	t.Run("real_run_reports_expired_host_credentials", testDoctorRealRunReportsExpiredHostCredentials)
	t.Run("real_run_reports_absent_host_credentials", testDoctorRealRunReportsAbsentHostCredentials)
	t.Run("real_run_reports_valid_host_credentials", testDoctorRealRunReportsValidHostCredentials)
	t.Run("real_run_reports_unrecorded_expiry_and_unresolved_region", testDoctorRealRunReportsUnrecordedExpiryAndUnresolvedRegion)
	t.Run("real_run_pod_down_degrades_host_credentials_instead_of_aborting", testDoctorRealRunPodDownDegradesHostCredentialsInsteadOfAborting)
}

func testDoctorDryRunAwsAliasEnvPlansHostCredentialCheck(t *testing.T) {
	// For an env configured to act as the operator's AWS identity, doctor
	// reads back the pod's erun-host profile. The plan must show the exec and
	// the script, and the script must ask only for presence and expiry —
	// never for a key, a secret, or a session token.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_aws_alias_env_plans_host_credential_check", normalize.Apply(result.Combined))
}

func testDoctorDryRunRemoteAgentEnvPlansGitPushAccessCheck(t *testing.T) {
	// A remote-agent env's project checkout lives inside the pod, so an
	// environment can look completely healthy (clone, build, test all work
	// against a public repo) and only discover it cannot push at the very
	// end of real work. doctor reads back fetch/push access so that
	// asymmetry surfaces up front instead. The plan must show the exec and
	// the read-only script -- it must never invoke `gh auth
	// login`/`refresh`/`switch`, which would start gh's interactive
	// device-code/browser flow and hang forever in a headless pod.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_remote_agent_env_plans_git_push_access_check", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsFetchWorksPushCredentialMissing(t *testing.T) {
	// A public GitHub repository fetches anonymously for the whole life of
	// a piece of work, so this is the exact asymmetry that hides until the
	// moment something tries to push. doctor must name it plainly and point
	// at a non-interactive fix (never suggesting `gh auth login` from an
	// unattended run).
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlWithGitPushAccess(t, stubs, "https://github.com/acme/example.git", "1", "0", "0")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_fetch_works_push_credential_missing", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsGitPushCredentialConfigured(t *testing.T) {
	// The healthy verdict: a resolved gh session (or SSH key / token) reads
	// as "credential configured" and offers no remedy.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlWithGitPushAccess(t, stubs, "https://github.com/acme/example.git", "1", "1", "1")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_git_push_credential_configured", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsFetchAlsoFails(t *testing.T) {
	// The other half: a private remote with no credential at all can't
	// even fetch. Fetch and push are reported as independent verdicts.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlWithGitPushAccess(t, stubs, "git@github.com:acme/private.git", "0", "0", "0")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_fetch_also_fails", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsExpiredHostCredentials(t *testing.T) {
	// The profile is present and well-formed
	// but its credentials lapsed overnight, which otherwise first surfaces as
	// a registry pull error inside whatever tool reached AWS first. The
	// kubectl stub answers the read script with a past expiry — the decision
	// input dry-run cannot supply — so doctor must name it as expired and
	// point at the refresh verb.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlWithHostCredentials(t, stubs, "present", "2020-01-02T03:04:05Z")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_expired_host_credentials", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsAbsentHostCredentials(t *testing.T) {
	// The other half of the same diagnosis: nothing ever injected the
	// profile, so the pod has no AWS identity at all.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlWithHostCredentials(t, stubs, "absent", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_absent_host_credentials", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsValidHostCredentials(t *testing.T) {
	// The healthy verdict: a future expiry reads as "valid until" and offers
	// no recovery, so a working env is not nagged at.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlWithHostCredentials(t, stubs, "present", "2126-01-02T03:04:05Z")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_valid_host_credentials", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsUnrecordedExpiryAndUnresolvedRegion(t *testing.T) {
	// A profile written by an older erun records no expiry, so staleness
	// cannot be judged and the report says so instead of claiming health.
	// The same env resolves no region — the other half of the failure the
	// operator hit — so the region line names that too.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "", "")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlWithHostCredentials(t, stubs, "present", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_reports_unrecorded_expiry_and_unresolved_region", normalize.Apply(result.Combined))
}

func testDoctorRealRunPodDownDegradesHostCredentialsInsteadOfAborting(t *testing.T) {
	// The runtime pod is up but its erun-devops container has terminated
	// (phase Running, reason Error) — exactly the situation doctor exists to
	// diagnose, so no check may abort by exec'ing into that down container
	// before reporting anything else. The helm/pod diagnosis must print first
	// (it needs no exec and is safe on a down pod), then the
	// host-credentials read must degrade to a "could not read" report
	// instead of failing the whole command, and every check after it
	// (the docker-storage inspection, prune) must still run against the
	// dind sidecar rather than being skipped.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorHelmStatus(t, stubs, "deployed")
	stubDoctorKubectlHostCredentialsExecFails(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/real_run_pod_down_degrades_host_credentials_instead_of_aborting", normalize.Apply(result.Combined))
}

// stubDoctorKubectlHostCredentialsExecFails stubs a pod-down state: the runtime pod
// is up but its erun-devops container is down, so the host-credentials exec
// fails with the same "container not found" shape kubectl reports for a
// missing container, while every other kubectl surface (the pod listing for
// the diagnosis, docker storage) stays healthy.
func stubDoctorKubectlHostCredentialsExecFails(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"x_erun_expiration"*)`,
		`    echo 'Defaulted container "erun-devops" out of: erun-devops, erun-dind, prepare-volumes (init)' >&2`,
		`    echo 'error: Internal error occurred: unable to upgrade connection: container not found ("erun-devops")' >&2`,
		`    exit 1`,
		`    ;;`,
		stubDoctorPodRequestsArm(),
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   0/2     Error   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`  *"df -h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Mounted on' 'overlay     100G  20G   80G    /var/lib/docker' ;;`,
		`  *"docker image prune"*) printf '%s\n' 'Total reclaimed space: 2GB' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}

// stubDoctorKubectlWithHostCredentials answers the host-credentials read script
// with a fixed presence/expiry verdict — the decision input dry-run cannot
// supply — while keeping every other doctor arm intact. Matched on the
// x_erun_expiration key, which only that script mentions.
func stubDoctorKubectlWithHostCredentials(t *testing.T, stubsDir, presence, expiration string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"x_erun_expiration"*) printf 'profile=` + presence + `\nexpiration=` + expiration + `\n' ;;`,
		stubDoctorPodRequestsArm(),
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   2/2     Running   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`  *"df -h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Mounted on' 'overlay     100G  20G   80G    /var/lib/docker' ;;`,
		`  *"docker image prune"*) printf '%s\n' 'Total reclaimed space: 2GB' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}

// stubDoctorKubectlWithGitPushAccess answers the git-push-access read script
// with a fixed remote/fetch/gh-auth/push-credential verdict — the decision
// input dry-run cannot supply — while keeping every other doctor arm intact.
// Matched on the push_credential key, which only that script prints.
func stubDoctorKubectlWithGitPushAccess(t *testing.T, stubsDir, remote, fetchOK, ghAuthenticated, pushCredential string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"push_credential="*) printf 'remote=` + remote + `\nfetch_ok=` + fetchOK + `\ngh_authenticated=` + ghAuthenticated + `\npush_credential=` + pushCredential + `\n' ;;`,
		stubDoctorPodRequestsArm(),
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   2/2     Running   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`  *"df -h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Mounted on' 'overlay     100G  20G   80G    /var/lib/docker' ;;`,
		`  *"docker image prune"*) printf '%s\n' 'Total reclaimed space: 2GB' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}
