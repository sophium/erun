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
	// The read script asks only for presence and expiry, never for key material.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_aws_alias_env_plans_host_credential_check", normalize.Apply(result.Combined))
}

func testDoctorDryRunRemoteAgentEnvPlansGitPushAccessCheck(t *testing.T) {
	// The read-only script must never run `gh auth login/refresh/switch`, which
	// would hang on an interactive flow in a headless pod.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_remote_agent_env_plans_git_push_access_check", normalize.Apply(result.Combined))
}

func testDoctorRealRunReportsFetchWorksPushCredentialMissing(t *testing.T) {
	// A public repo fetches anonymously, so only push lacks a credential; the
	// remedy must be non-interactive.
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
	// The kubectl stub supplies a past expiry, which dry-run cannot.
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
	// An older erun recorded no expiry, so staleness cannot be judged.
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
	// The erun-devops container is down: no check may abort the run, and the dind
	// checks still run.
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

// stubDoctorKubectlHostCredentialsExecFails fails only the erun-devops exec with
// "container not found".
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

// stubDoctorKubectlWithHostCredentials answers the host-credentials script
// (matched on x_erun_expiration) with a fixed verdict.
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

// stubDoctorKubectlWithGitPushAccess answers the git-push-access script
// (matched on push_credential) with a fixed verdict.
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
