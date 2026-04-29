package cmd

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	common "github.com/sophium/erun/erun-common"
)

func TestKubectlSSHDPortForwardArgs(t *testing.T) {
	got := kubectlPortForwardArgs(common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "dev",
		EnvConfig: common.EnvConfig{
			KubernetesContext: "cluster-dev",
		},
		LocalPorts: common.EnvironmentLocalPorts{
			RangeStart: 17100,
			RangeEnd:   17199,
			MCP:        17100,
			SSH:        17122,
		},
	}, 17122)

	want := []string{
		"--context", "cluster-dev",
		"--namespace", "tenant-a-dev",
		"port-forward",
		"deployment/tenant-a-devops",
		"17122:17122",
		"--address", "127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args:\ngot:  %v\nwant: %v", got, want)
	}
}

func TestCanReachLocalSSHEndpointRequiresSSHBanner(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		_ = listener.Close()
	}()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		_, _ = conn.Write([]byte("SSH-2.0-test\r\n"))
	}()

	_, portValue, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	if !canReachLocalSSHEndpoint(port) {
		t.Fatal("expected SSH endpoint to be reachable")
	}
}

func TestRunSSHDInitCommandPersistsConfigAndDeploysRuntime(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	publicKeyPath := filepath.Join(t.TempDir(), "id_ed25519.pub")
	requireNoError(t, os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644), "write public key")

	var savedTenant string
	var savedEnv common.EnvConfig
	var deployed common.HelmDeployParams
	var remoteScript string
	ctx := common.Context{
		Logger: common.NewLoggerWithWriters(1, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	err := runSSHDInitCommand(
		ctx,
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name: "tenant-a",
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
		},
		publicKeyPath,
		64022,
		func(tenant string, config common.EnvConfig) error {
			savedTenant = tenant
			savedEnv = config
			return nil
		},
		func(target common.OpenResult) (common.DeploySpec, error) {
			return common.DeploySpec{
				Target: target,
				Deploy: common.HelmDeploySpec{
					ReleaseName:       common.RuntimeReleaseName(target.Tenant),
					ChartPath:         "/tmp/chart",
					ValuesFilePath:    "/tmp/chart/values.dev.yaml",
					Tenant:            target.Tenant,
					Environment:       target.Environment,
					Namespace:         common.KubernetesNamespaceName(target.Tenant, target.Environment),
					KubernetesContext: target.EnvConfig.KubernetesContext,
					WorktreeStorage:   common.WorktreeStoragePVC,
					WorktreeRepoName:  "tenant-a",
					WorktreeHostPath:  "/tmp/ignored",
					SSHDEnabled:       target.EnvConfig.SSHD.Enabled,
					Timeout:           common.DefaultHelmDeploymentTimeout,
				},
			}, nil
		},
		func(params common.HelmDeployParams) error {
			deployed = params
			return nil
		},
		func(_ common.ShellLaunchParams, script string) (common.RemoteCommandResult, error) {
			remoteScript = script
			return common.RemoteCommandResult{}, nil
		},
		writeLocalSSHConfig,
	)
	requireNoError(t, err, "runSSHDInitCommand failed")

	requireSSHDInitConfig(t, savedTenant, savedEnv, deployed, remoteScript, publicKeyPath)
	sshConfigData, err := os.ReadFile(filepath.Join(homeDir, ".ssh", "config"))
	requireNoError(t, err, "read ssh config")
	requireContainsAll(t, string(sshConfigData), []string{
		"Host erun-tenant-a-dev",
		"HostName 127.0.0.1",
		"Port 64022",
		"User erun",
		"HostKeyAlias erun-tenant-a-dev",
		"IdentityFile " + strings.TrimSuffix(publicKeyPath, ".pub"),
	}, "ssh config")
	stdout := ctx.Stdout.(*bytes.Buffer).String()
	requireContainsAll(t, stdout, []string{
		"host: erun-tenant-a-dev",
		"config: " + filepath.Join(homeDir, ".ssh", "config"),
	}, "stdout")
}

func requireSSHDInitConfig(t *testing.T, savedTenant string, savedEnv common.EnvConfig, deployed common.HelmDeployParams, remoteScript, publicKeyPath string) {
	t.Helper()
	if savedTenant != "tenant-a" {
		t.Fatalf("unexpected saved tenant: %q", savedTenant)
	}
	if !savedEnv.SSHD.Enabled || savedEnv.SSHD.LocalPort != 64022 || savedEnv.SSHD.PublicKeyPath != publicKeyPath {
		t.Fatalf("unexpected saved env config: %+v", savedEnv)
	}
	if !deployed.SSHDEnabled {
		t.Fatalf("expected deployment params to enable SSHD, got %+v", deployed)
	}
	if !strings.Contains(remoteScript, "authorized_keys") || !strings.Contains(remoteScript, "ssh-ed25519 AAAATEST user@example") {
		t.Fatalf("unexpected remote authorized_keys script:\n%s", remoteScript)
	}
}

func requireContainsAll(t *testing.T, value string, wants []string, context string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(value, want) {
			t.Fatalf("expected %s to contain %q, got:\n%s", context, want, value)
		}
	}
}

func TestRunSSHDInitCommandUsesResolvedEnvironmentLocalPortByDefault(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	publicKeyPath := filepath.Join(t.TempDir(), "id_ed25519.pub")
	requireNoError(t, os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644), "write public key")

	var savedEnv common.EnvConfig
	err := runSSHDInitCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(1, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "prod",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name: "tenant-a",
			},
			EnvConfig: common.EnvConfig{
				Name:              "prod",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-prod",
				Remote:            true,
			},
			LocalPorts: common.EnvironmentLocalPorts{
				RangeStart: 17100,
				RangeEnd:   17199,
				MCP:        17100,
				SSH:        17122,
			},
		},
		publicKeyPath,
		0,
		func(_ string, config common.EnvConfig) error {
			savedEnv = config
			return nil
		},
		func(target common.OpenResult) (common.DeploySpec, error) {
			return common.DeploySpec{
				Target: target,
				Deploy: common.HelmDeploySpec{
					ReleaseName:       common.RuntimeReleaseName(target.Tenant),
					ChartPath:         "/tmp/chart",
					ValuesFilePath:    "/tmp/chart/values.prod.yaml",
					Tenant:            target.Tenant,
					Environment:       target.Environment,
					Namespace:         common.KubernetesNamespaceName(target.Tenant, target.Environment),
					KubernetesContext: target.EnvConfig.KubernetesContext,
					SSHDEnabled:       target.EnvConfig.SSHD.Enabled,
					Timeout:           common.DefaultHelmDeploymentTimeout,
				},
			}, nil
		},
		func(common.HelmDeployParams) error { return nil },
		func(_ common.ShellLaunchParams, _ string) (common.RemoteCommandResult, error) {
			return common.RemoteCommandResult{}, nil
		},
		func(common.OpenResult) (SSHDLocalConfigResult, error) {
			return SSHDLocalConfigResult{}, nil
		},
	)
	if err != nil {
		t.Fatalf("runSSHDInitCommand failed: %v", err)
	}
	if savedEnv.SSHD.LocalPort != 17122 {
		t.Fatalf("expected resolved environment SSH port, got %+v", savedEnv.SSHD)
	}
}

func TestSyncRemoteSSHDKeyRetriesWhenDeploymentIsNotReady(t *testing.T) {
	prevWait := waitForSSHDRemoteDeployment
	prevSleep := sleepBeforeSSHDRemoteExecRetry
	t.Cleanup(func() {
		waitForSSHDRemoteDeployment = prevWait
		sleepBeforeSSHDRemoteExecRetry = prevSleep
	})

	publicKeyPath := filepath.Join(t.TempDir(), "id_ed25519.pub")
	requireNoError(t, os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644), "write public key")

	var waited common.ShellLaunchParams
	waitForSSHDRemoteDeployment = func(req common.ShellLaunchParams) error {
		waited = req
		return nil
	}
	sleepBeforeSSHDRemoteExecRetry = func(time.Duration) {}

	attempts := 0
	got, err := syncRemoteSSHDKey(
		common.Context{
			Logger: common.NewLoggerWithWriters(1, new(bytes.Buffer), new(bytes.Buffer)),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name: "tenant-a",
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD: common.SSHDConfig{
					Enabled:       true,
					PublicKeyPath: publicKeyPath,
				},
			},
		},
		func(_ common.ShellLaunchParams, _ string) (common.RemoteCommandResult, error) {
			attempts++
			if attempts == 1 {
				return common.RemoteCommandResult{
					Stderr: `Defaulted container "petios-devops" out of: petios-devops, erun-dind, install-binfmt (init)
error: Internal error occurred: unable to upgrade connection: container not found ("petios-devops")`,
				}, errors.New("exit status 1")
			}
			return common.RemoteCommandResult{}, nil
		},
	)
	if err != nil {
		t.Fatalf("syncRemoteSSHDKey failed: %v", err)
	}
	if got != publicKeyPath {
		t.Fatalf("unexpected key path: %q", got)
	}
	if attempts != 2 {
		t.Fatalf("expected retry after deployment readiness failure, got %d attempts", attempts)
	}
	if waited.Namespace != "tenant-a-dev" || waited.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected wait params: %+v", waited)
	}
}

func TestSyncRemoteSSHDKeyRetriesWhenKubeletProxyIsStarting(t *testing.T) {
	prevWait := waitForSSHDRemoteDeployment
	prevSleep := sleepBeforeSSHDRemoteExecRetry
	t.Cleanup(func() {
		waitForSSHDRemoteDeployment = prevWait
		sleepBeforeSSHDRemoteExecRetry = prevSleep
	})

	publicKeyPath := filepath.Join(t.TempDir(), "id_ed25519.pub")
	requireNoError(t, os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644), "write public key")

	waits := 0
	waitForSSHDRemoteDeployment = func(common.ShellLaunchParams) error {
		waits++
		return nil
	}
	sleeps := 0
	sleepBeforeSSHDRemoteExecRetry = func(time.Duration) {
		sleeps++
	}

	attempts := 0
	_, err := syncRemoteSSHDKey(
		common.Context{
			Logger: common.NewLoggerWithWriters(1, new(bytes.Buffer), new(bytes.Buffer)),
		},
		common.OpenResult{
			Tenant:      "petios",
			Environment: "rihards",
			RepoPath:    "/home/erun/git/petios",
			TenantConfig: common.TenantConfig{
				Name: "petios",
			},
			EnvConfig: common.EnvConfig{
				Name:              "rihards",
				RepoPath:          "/home/erun/git/petios",
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD: common.SSHDConfig{
					Enabled:       true,
					PublicKeyPath: publicKeyPath,
				},
			},
		},
		func(_ common.ShellLaunchParams, _ string) (common.RemoteCommandResult, error) {
			attempts++
			if attempts == 1 {
				return common.RemoteCommandResult{
					Stderr: `error: Internal error occurred: error sending request: Post "https://172.31.31.130:10250/exec/petios-rihards/petios-devops-59d995466-l9sbh/petios-devops": proxy error from 127.0.0.1:6443 while dialing 172.31.31.130:10250, code 502: 502 Bad Gateway`,
				}, errors.New("exit status 1")
			}
			return common.RemoteCommandResult{}, nil
		},
	)
	if err != nil {
		t.Fatalf("syncRemoteSSHDKey failed: %v", err)
	}
	if attempts != 2 || waits != 1 || sleeps != 1 {
		t.Fatalf("unexpected retry counts: attempts=%d waits=%d sleeps=%d", attempts, waits, sleeps)
	}
}

func TestSSHDRemoteExecNeedsDeploymentRetry(t *testing.T) {
	tests := []string{
		"error: Internal error occurred: unable to upgrade connection: pod does not exist",
		`error: error upgrading connection: unable to upgrade connection: pod not found ("petios-devops-123")`,
		"error: lost connection to pod",
		`Defaulted container "petios-devops" out of: petios-devops, erun-dind, install-binfmt (init)
error: Internal error occurred: unable to upgrade connection: container not found ("petios-devops")`,
		`error: Internal error occurred: error sending request: proxy error from 127.0.0.1:6443 while dialing 172.31.31.130:10250, code 502: 502 Bad Gateway`,
	}
	for _, stderr := range tests {
		if !sshdRemoteExecNeedsDeploymentRetry(stderr) {
			t.Fatalf("expected stderr to be classified as deployment readiness failure: %s", stderr)
		}
	}
}
