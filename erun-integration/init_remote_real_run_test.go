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
	// -y approves the host-config reuse and keeps the run to the one prompt readline read-ahead allows.
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
	// No stdin: the absence of any prompt is part of the contract.
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
	// Init must refuse right after the deploy, before any git/SSH setup.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RegistryCredentialConfigured: false})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	// Anonymously pullable keeps the host-side pull-secret preflight a no-op.
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
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RegistryCredentialConfigured: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	// Anonymously pullable keeps the host-side pull-secret preflight a no-op.
	envVars = append(envVars, "ERUN_ANONYMOUS_PULLABLE_OVERRIDE=sophium/team-devops:1.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "ghcr.io/sophium",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--no-git",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/remote_real_run_ghcr_registry_credential_configured_succeeds", normalize.Apply(result.Combined))
}

func testInitRemoteRealRunCodecommitKeyImportRetry(t *testing.T) {
	// The key ID comes from the flag because a second prompt is unreachable over piped stdin.
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
