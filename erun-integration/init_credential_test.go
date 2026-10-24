package integration

import (
	"encoding/base64"
	"fmt"
	"net/http"
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

func initCredentialProvisioningScenarios(t *testing.T) {
	t.Run("remote_dry_run_traces_registry_credential_check", testInitRemoteDryRunTracesRegistryCredentialCheck)
	t.Run("remote_dry_run_provisions_registry_credential_from_host", testInitRemoteDryRunProvisionsRegistryCredentialFromHost)
	t.Run("remote_dry_run_traces_machine_identity_provisioning", testInitRemoteDryRunTracesMachineIdentityProvisioning)
	t.Run("remote_real_run_provisions_the_environments_own_machine_identity", testInitRemoteRealRunProvisionsTheEnvironmentsOwnMachineIdentity)
	t.Run("remote_real_run_falls_back_when_the_minted_credential_is_refused", testInitRemoteRealRunFallsBackWhenTheMintedCredentialIsRefused)
	t.Run("remote_real_run_ignores_an_identity_with_no_credential", testInitRemoteRealRunIgnoresAnIdentityWithNoCredential)
	t.Run("remote_real_run_stays_on_the_delegated_alias_when_the_platform_refuses_to_mint", testInitRemoteRealRunStaysOnTheDelegatedAliasWhenThePlatformRefusesToMint)
}

func testInitRemoteDryRunTracesRegistryCredentialCheck(t *testing.T) {
	// A preview must not fail over pod state that does not exist yet.
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "ghcr.io/sophium",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--no-git",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/remote_dry_run_traces_registry_credential_check", normalize.Apply(result.Combined))
}

func testInitRemoteDryRunProvisionsRegistryCredentialFromHost(t *testing.T) {
	// DOCKER_CONFIG isolates the host credential resolver from the developer's ~/.docker.
	setup := env.New(t)
	dockerCfgDir := filepath.Join(setup.Cwd, "docker-inline")
	if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir docker config dir: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("sophium:s3cret-token"))
	dockerCfg := fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, encoded)
	if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(dockerCfg), 0o644); err != nil {
		t.Fatalf("write docker config: %v", err)
	}

	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "ghcr.io/sophium",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--no-git",
		"--dry-run",
	}
	envVars := append(setup.Env(), "DOCKER_CONFIG="+dockerCfgDir)
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}
	golden.Equal(t, "init/remote_dry_run_provisions_registry_credential_from_host", normalize.Apply(result.Combined))
}

func testInitRemoteDryRunTracesMachineIdentityProvisioning(t *testing.T) {
	// A dry run names the identity request but must not mint anything.
	stub := &machineIdentityStub{}
	server := machineIdentityServer(t, stub)
	setup := env.New(t)
	fixture.SeedSignedInERunPlatformAlias(t, setup, "team", "erun+test@erun", server.URL)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--no-git",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if stub.mintCount() != 0 {
		t.Fatalf("a dry run must not ask the platform to mint anything, got %d mint calls", stub.mintCount())
	}
	golden.Equal(t, "init/remote_dry_run_traces_machine_identity_provisioning", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
}

func testInitRemoteRealRunProvisionsTheEnvironmentsOwnMachineIdentity(t *testing.T) {
	// The minted credential is exchanged for a token before anything is written.
	stub := &machineIdentityStub{}
	server := machineIdentityServer(t, stub)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	fixture.SeedSignedInERunPlatformAlias(t, setup, "team", "erun+test@erun", server.URL)
	seedHostPlatformAccessToken(t, setup, "erun+test@erun")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "2.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if stub.mintCount() != 1 {
		t.Fatalf("want exactly one mint call, got %d", stub.mintCount())
	}
	if strings.Contains(result.Combined, machineIdentityClientSecret) {
		t.Fatalf("the minted client secret must never appear in trace output: %s", result.Combined)
	}
	assertEnvConfigContains(t, setup, "team", "dev", "platformaliassecretname: team-devops-platform-alias")
	golden.Equal(t, "init/remote_real_run_provisions_the_environments_own_machine_identity", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
}

func testInitRemoteRealRunFallsBackWhenTheMintedCredentialIsRefused(t *testing.T) {
	// A machine identity nothing accepts is worse than none, so the delegated alias stays.
	stub := &machineIdentityStub{TokenRefusal: http.StatusUnauthorized}
	server := machineIdentityServer(t, stub)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	fixture.SeedSignedInERunPlatformAlias(t, setup, "team", "erun+test@erun", server.URL)
	seedHostPlatformAccessToken(t, setup, "erun+test@erun")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "2.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "could not be exchanged for a token") {
		t.Fatalf("the fall-back must say why it fell back: %s", result.Combined)
	}
	golden.Equal(t, "init/remote_real_run_falls_back_when_the_minted_credential_is_refused", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
}

func testInitRemoteRealRunIgnoresAnIdentityWithNoCredential(t *testing.T) {
	// An identity with no secret would leave the pod referencing an undelivered credential.
	stub := &machineIdentityStub{MintOmitCredential: true}
	server := machineIdentityServer(t, stub)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	fixture.SeedSignedInERunPlatformAlias(t, setup, "team", "erun+test@erun", server.URL)
	seedHostPlatformAccessToken(t, setup, "erun+test@erun")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "2.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "returned no usable credential") {
		t.Fatalf("the missing credential must be traced rather than written: %s", result.Combined)
	}
	golden.Equal(t, "init/remote_real_run_ignores_an_identity_with_no_credential", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
}

func testInitRemoteRealRunStaysOnTheDelegatedAliasWhenThePlatformRefusesToMint(t *testing.T) {
	// A 409 MACHINE_IDENTITY_UNAVAILABLE is a decline, not an init failure.
	stub := &machineIdentityStub{MintRefusal: &machineIdentityRefusal{Status: http.StatusConflict, Code: "MACHINE_IDENTITY_UNAVAILABLE"}}
	server := machineIdentityServer(t, stub)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	fixture.SeedSignedInERunPlatformAlias(t, setup, "team", "erun+test@erun", server.URL)
	seedHostPlatformAccessToken(t, setup, "erun+test@erun")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "2.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "would not mint one") {
		t.Fatalf("the decline must be traced rather than swallowed: %s", result.Combined)
	}
	golden.Equal(t, "init/remote_real_run_stays_on_the_delegated_alias_when_the_platform_refuses_to_mint", normalize.Apply(result.Combined, stubServerRule(server, "<IDENTITY_API>")))
}
