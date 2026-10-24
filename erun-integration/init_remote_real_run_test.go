package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func initRemoteRealRunScenarios(t *testing.T) {
	t.Run("remote_real_run_reuses_host_ssh_config", testInitRemoteRealRunReusesHostSshConfig)
	t.Run("remote_real_run_host_config_rejected_falls_back_to_key_import", testInitRemoteRealRunHostConfigRejectedFallsBackToKeyImport)
	t.Run("remote_real_run_existing_repo_pulls", testInitRemoteRealRunExistingRepoPulls)
	t.Run("remote_real_run_ghcr_registry_missing_credential_refuses", testInitRemoteRealRunGhcrRegistryMissingCredentialRefuses)
	t.Run("remote_real_run_ghcr_registry_credential_configured_succeeds", testInitRemoteRealRunGhcrRegistryCredentialConfiguredSucceeds)
	t.Run("remote_real_run_codecommit_key_import_retry", testInitRemoteRealRunCodecommitKeyImportRetry)
	t.Run("remote_real_run_codecommit_key_id_in_url", testInitRemoteRealRunCodecommitKeyIdInUrl)
}

func testInitRemoteRealRunReusesHostSshConfig(t *testing.T) {
	// Must be a real run: reusing an existing ~/.ssh/config and skipping the
	// per-key credential flow depends on the pod's live answers, which
	// dry-run would hardcode. The repository URL has no CLI flag, so it comes
	// over stdin, and -y both auto-approves the host-config reuse and keeps
	// the run to the single prompt readline read-ahead allows.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{SSHConfigExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"-y",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "ssh://git.example.com/team/repo.git\n",
	})
	golden.Equal(t, "init/remote_real_run_reuses_host_ssh_config", normalize.Apply(result.Combined))
}

func testInitRemoteRealRunHostConfigRejectedFallsBackToKeyImport(t *testing.T) {
	// When the pod's existing ~/.ssh/config fails verification, init must
	// fall back to the per-key credential flow: print the pod's public key,
	// wait for the user to import it, poll access, then clone. The repository
	// URL has no CLI flag, so it comes over stdin.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{
		SSHConfigExists:          true,
		HostConfigVerifyExitCode: 1,
	})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"-y",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "ssh://git.example.com/team/repo.git\n",
	})
	golden.Equal(t, "init/remote_real_run_host_config_rejected_falls_back_to_key_import", normalize.Apply(result.Combined))
}

func testInitRemoteRealRunExistingRepoPulls(t *testing.T) {
	// When the pod already has a clone, init must pull instead of prompting
	// for a URL or touching SSH credentials. No stdin: the absence of any
	// prompt is part of the contract this scenario locks.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "init/remote_real_run_existing_repo_pulls", normalize.Apply(result.Combined))
}

func testInitRemoteRealRunGhcrRegistryMissingCredentialRefuses(t *testing.T) {
	// #1201: init deployed a pod configured to build+push to ghcr.io, but the
	// pod itself never authenticated (no docker config, no gh session, no
	// GH_TOKEN). Before this fix, init reported success and the failure only
	// surfaced 7 minutes into the first `erun release`, at the push. Init must
	// now refuse right after the deploy, before ever touching git/SSH setup --
	// no repository state probe, no SSH key output.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RegistryCredentialConfigured: false})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	// This scenario is about the in-pod registry credential check above, not
	// the separate deploy-time pull-secret preflight (which runs first and
	// checks the *host's* own ghcr.io credential): mark the tenant's runtime
	// image anonymously pullable so that preflight is a no-op and the deploy
	// reaches the in-pod check this scenario actually exercises.
	envVars = append(envVars, "ERUN_ANONYMOUS_PULLABLE_OVERRIDE=sophium/team-devops:1.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "ghcr.io/sophium",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the pod has no ghcr.io credential, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/remote_real_run_ghcr_registry_missing_credential_refuses", normalize.Apply(result.Combined))
}

func testInitRemoteRealRunGhcrRegistryCredentialConfiguredSucceeds(t *testing.T) {
	// The other half of #1201: a pod that already has a ghcr.io credential
	// (the operator authenticated it, or it inherited one from a prior push)
	// must not be blocked by the new check -- init completes normally.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RegistryCredentialConfigured: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	// As above: this scenario is about the in-pod registry credential check,
	// not the separate host-side deploy-time pull-secret preflight, so mark
	// the tenant's runtime image anonymously pullable to keep that preflight
	// a no-op.
	envVars = append(envVars, "ERUN_ANONYMOUS_PULLABLE_OVERRIDE=sophium/team-devops:1.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "ghcr.io/sophium",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		// No-git keeps this scenario about the credential check alone; git
		// checkout setup is covered by the other remote real-run scenarios.
		"--no-git",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/remote_real_run_ghcr_registry_credential_configured_succeeds", normalize.Apply(result.Combined))
}

func testInitRemoteRealRunCodecommitKeyImportRetry(t *testing.T) {
	// CodeCommit real-run: init prints the pod's CodeCommit key with IAM
	// upload instructions, then polls access — the stub fails the first
	// ls-remote to drive the retry loop once before the clone. The key ID
	// comes from --codecommit-ssh-key-id because the interactive key-ID
	// prompt would be a second promptui prompt, unreachable over piped stdin.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{LsRemoteFailures: 1})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--codecommit-ssh-key-id", "APKAEXAMPLEKEYID",
		"-y",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/team\n",
	})
	golden.Equal(t, "init/remote_real_run_codecommit_key_import_retry", normalize.Apply(result.Combined))
}

func testInitRemoteRealRunCodecommitKeyIdInUrl(t *testing.T) {
	// The CodeCommit SSH key ID can ride in the URL's user part (the form the
	// AWS console hands out): init must extract it, strip it from the clone
	// URL, and accept it without the --codecommit-ssh-key-id flag or a key
	// prompt. Access succeeds on the first poll, so no retry lines appear.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"-y",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "ssh://APKAEXAMPLEKEYID@git-codecommit.eu-west-1.amazonaws.com/v1/repos/team\n",
	})
	golden.Equal(t, "init/remote_real_run_codecommit_key_id_in_url", normalize.Apply(result.Combined))
}
