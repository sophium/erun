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
	// Dry-run must show the new check as a trace line like every other
	// action init takes, and must not fail the plan preview over pod state a
	// preview cannot know (the pod does not exist yet in dry-run).
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
	// When the host running `erun init` already has a ghcr.io credential
	// (docker config, gh session, or GH_TOKEN), init mints the
	// dockerconfigjson Secret the runtime chart mounts, rather than leaving
	// the pod to hand-carry one. DOCKER_CONFIG points the host resolver at
	// this isolated dir instead of the developer's real ~/.docker, the same
	// seam TestVersion's docker-config scenarios use.
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
	// The host running init is signed in to an erun platform alias, so the
	// environment can be given its own identity instead of a delegated copy of
	// the operator's session. A preview can only name what it would ask for:
	// both the environment's platform id and the identity itself are
	// server-side facts, so the trace has to carry the request rather than a
	// credential it could not have. Nothing is minted -- a dry run that called
	// the platform would be creating state it promised not to.
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
	// The point of the whole feature: a fresh environment's platform calls are
	// attributed to the environment, not to whoever ran init. The host's own
	// signed-in alias is what asks for the identity -- it is the only
	// credential this process holds -- and the credential that comes back is
	// exchanged for a token before anything is written, so the environment
	// never trades a working identity for one nothing accepts. The Secret it
	// lands in is the same one the delegated path mints, because an environment
	// has one platform identity and the entry inside says which kind it is.
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
	// The platform minted an identity but the issuer it named would not accept
	// the credential -- an IdP the platform's own binding disagreed with, or a
	// clock far enough out to expire the assertion on arrival. A machine
	// identity nothing accepts is worse than none: it replaces a working
	// delegated session with a pod that can reach nothing. So the run keeps the
	// delegated alias and says so, and the environment stays exactly as capable
	// as it was before this existed.
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
	// The platform answered 200 with an identity row but no client
	// credential in it -- a plane that recorded the identity and could not
	// return its secret. There is nothing to write, and writing an alias entry
	// whose secret is absent would leave the pod holding a reference to a
	// credential that was never delivered, so the run says so and keeps the
	// delegated session.
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
	// A tenant the platform will not mint an identity for -- one resolving by
	// no issuer it administers is the case the API answers 409
	// MACHINE_IDENTITY_UNAVAILABLE for. That is a decline, not a failure of the
	// init: the environment still gets the operator's delegated session, which
	// is exactly what it would have had before the machine identity existed.
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
