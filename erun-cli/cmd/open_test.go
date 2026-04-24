package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

func TestOpenCommandLaunchesShell(t *testing.T) {
	repoPath := t.TempDir()
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestOpenHelpShowsTenantAndEnvironmentFlags(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{})
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"open", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"--tenant string",
		"Open a specific tenant",
		"--environment string",
		"Open a specific environment",
		"--vscode",
		"Open the remote environment in VS Code instead of a shell",
		"--intellij",
		"Open the remote environment in IntelliJ IDEA instead of a shell",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected open help to contain %q, got:\n%s", want, output)
		}
	}
}

func TestOpenCommandAcceptsTenantAndEnvironmentFlags(t *testing.T) {
	repoPath := t.TempDir()
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "--tenant", "tenant-a", "--environment", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

func TestRunResolvedOpenCommandActivatesSSHDWhenEnabled(t *testing.T) {
	activated := false
	launched := false
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name:     "tenant-a",
				Remote:   true,
				Snapshot: nil,
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD:              common.SSHDConfig{Enabled: true},
			},
		},
		openOptions{},
		nil,
		func(_ common.Context, _ common.ShellLaunchParams) error {
			launched = true
			return nil
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		func(_ common.Context, result common.OpenResult) error {
			activated = true
			if !result.EnvConfig.SSHD.Enabled {
				t.Fatalf("expected SSHD-enabled target, got %+v", result.EnvConfig.SSHD)
			}
			return nil
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runResolvedOpenCommand failed: %v", err)
	}
	if !activated {
		t.Fatal("expected SSHD activator to run")
	}
	if !launched {
		t.Fatal("expected shell launch after SSHD activation")
	}
}

func TestRunResolvedOpenCommandForwardsMCPBeforeShell(t *testing.T) {
	forwarded := false
	launched := false
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name:   "tenant-a",
				Remote: true,
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
			LocalPorts: common.EnvironmentLocalPorts{
				RangeStart: 17100,
				RangeEnd:   17199,
				MCP:        17100,
				SSH:        17122,
			},
		},
		openOptions{},
		nil,
		func(_ common.Context, _ common.ShellLaunchParams) error {
			if !forwarded {
				t.Fatal("expected MCP forward before shell launch")
			}
			launched = true
			return nil
		},
		nil,
		nil,
		nil,
		nil,
		func(_ common.Context, result common.OpenResult) error {
			forwarded = true
			if common.MCPPortForResult(result) != 17100 {
				t.Fatalf("unexpected MCP port: %+v", result.LocalPorts)
			}
			return nil
		},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runResolvedOpenCommand failed: %v", err)
	}
	if !forwarded {
		t.Fatal("expected MCP forwarder to run")
	}
	if !launched {
		t.Fatal("expected shell launch")
	}
}

func TestRunResolvedOpenCommandForwardsMCPWithNoShell(t *testing.T) {
	forwarded := false
	stdout := new(bytes.Buffer)
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: stdout,
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
		},
		openOptions{NoShell: true},
		nil,
		func(_ common.Context, _ common.ShellLaunchParams) error {
			t.Fatal("did not expect shell launch")
			return nil
		},
		nil,
		nil,
		nil,
		nil,
		func(common.Context, common.OpenResult) error {
			forwarded = true
			return nil
		},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runResolvedOpenCommand failed: %v", err)
	}
	if !forwarded {
		t.Fatal("expected MCP forwarder to run")
	}
	if !strings.Contains(stdout.String(), "kubectl config use-context 'cluster-dev'") {
		t.Fatalf("expected no-shell setup output, got:\n%s", stdout.String())
	}
}

func TestEmitSSHDConnectionInfoIncludesHostAlias(t *testing.T) {
	stdout := new(bytes.Buffer)
	err := emitSSHDConnectionInfo(common.Context{Stdout: stdout}, common.SSHConnectionInfo{
		User:           "erun",
		Host:           "127.0.0.1",
		HostAlias:      "erun-tenant-a-dev",
		Port:           62222,
		PrivateKeyPath: "/Users/test/.ssh/id_ed25519",
		WorkspacePath:  "/home/erun/git/tenant-a",
	})
	if err != nil {
		t.Fatalf("emitSSHDConnectionInfo failed: %v", err)
	}
	for _, want := range []string{
		"host: 127.0.0.1",
		"alias: erun-tenant-a-dev",
		"key: /Users/test/.ssh/id_ed25519",
		"workspace: /home/erun/git/tenant-a",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected SSH output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

func TestRunResolvedOpenCommandForcesSSHDEnabledOnRuntimeDeploy(t *testing.T) {
	deployed := common.HelmDeployParams{}
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name:   "tenant-a",
				Remote: true,
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD:              common.SSHDConfig{Enabled: true},
			},
		},
		openOptions{},
		nil,
		func(_ common.Context, _ common.ShellLaunchParams) error { return nil },
		nil,
		func(common.KubernetesDeploymentCheckParams) (bool, error) {
			return false, nil
		},
		func(common.OpenResult) (common.DeploySpec, error) {
			return common.DeploySpec{
				Deploy: common.HelmDeploySpec{
					ReleaseName:       "tenant-a-devops",
					ChartPath:         "/tmp/chart",
					ValuesFilePath:    "/tmp/chart/values.dev.yaml",
					Tenant:            "tenant-a",
					Environment:       "dev",
					Namespace:         "tenant-a-dev",
					KubernetesContext: "cluster-dev",
					WorktreeStorage:   common.WorktreeStoragePVC,
					WorktreeRepoName:  "tenant-a",
					WorktreeHostPath:  "/tmp/ignored",
					SSHDEnabled:       false,
					Timeout:           common.DefaultHelmDeploymentTimeout,
				},
			}, nil
		},
		func(params common.HelmDeployParams) error {
			deployed = params
			return nil
		},
		nil,
		func(_ common.Context, _ common.OpenResult) error { return nil },
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runResolvedOpenCommand failed: %v", err)
	}
	if !deployed.SSHDEnabled {
		t.Fatalf("expected runtime deploy to force SSHD enabled, got %+v", deployed)
	}
}

func TestOpenCommandNoShellConfiguresLocalKubeconfig(t *testing.T) {
	repoPath := t.TempDir()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			t.Fatalf("did not expect deployment check for --no-shell: %+v", req)
			return false, nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch for --no-shell: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"open", "tenant-a", "dev", "--no-shell"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	expected := "kubectl config use-context 'cluster-dev' >/dev/null &&\n" +
		"kubectl config set-context --current --namespace='tenant-a-dev' >/dev/null &&\n" +
		"cd '" + repoPath + "'\n"
	if stdout.String() != expected {
		t.Fatalf("unexpected no-shell output:\nwant:\n%s\ngot:\n%s", expected, stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("did not expect stderr output in buffered mode, got %q", stderr.String())
	}
}

func TestRunResolvedOpenCommandLaunchesVSCodeWhenRequested(t *testing.T) {
	activated := false
	launched := false
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name:   "tenant-a",
				Remote: true,
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD:              common.SSHDConfig{Enabled: true},
			},
		},
		openOptions{VSCode: true},
		nil,
		func(_ common.Context, _ common.ShellLaunchParams) error {
			t.Fatal("did not expect shell launch")
			return nil
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		func(_ common.Context, _ common.OpenResult) error {
			activated = true
			return nil
		},
		func(_ common.Context, result common.OpenResult) error {
			launched = true
			if result.Tenant != "tenant-a" || result.Environment != "dev" {
				t.Fatalf("unexpected VS Code target: %+v", result)
			}
			return nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("runResolvedOpenCommand failed: %v", err)
	}
	if !activated {
		t.Fatal("expected SSH activation before launching VS Code")
	}
	if !launched {
		t.Fatal("expected VS Code launch")
	}
}

func TestRunResolvedOpenCommandRejectsVSCodeWithoutSSHD(t *testing.T) {
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
		},
		openOptions{VSCode: true},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected VS Code SSH error")
	}
	if got := err.Error(); !strings.Contains(got, "run `erun sshd init tenant-a dev` first") {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestRunResolvedOpenCommandLaunchesIntelliJWhenRequested(t *testing.T) {
	activated := false
	launched := false
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name:   "tenant-a",
				Remote: true,
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD:              common.SSHDConfig{Enabled: true},
			},
		},
		openOptions{IntelliJ: true},
		nil,
		func(_ common.Context, _ common.ShellLaunchParams) error {
			t.Fatal("did not expect shell launch")
			return nil
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		func(_ common.Context, _ common.OpenResult) error {
			activated = true
			return nil
		},
		nil,
		func(_ common.Context, result common.OpenResult, _ PromptRunner) error {
			launched = true
			if result.Tenant != "tenant-a" || result.Environment != "dev" {
				t.Fatalf("unexpected IntelliJ target: %+v", result)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runResolvedOpenCommand failed: %v", err)
	}
	if !activated {
		t.Fatal("expected SSH activation before launching IntelliJ")
	}
	if !launched {
		t.Fatal("expected IntelliJ launch")
	}
}

func TestRunResolvedOpenCommandRejectsIntelliJWithoutSSHD(t *testing.T) {
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
		},
		openOptions{IntelliJ: true},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected IntelliJ SSH error")
	}
	if got := err.Error(); !strings.Contains(got, "run `erun sshd init tenant-a dev` first") {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestRunResolvedOpenCommandRejectsMultipleIDETargets(t *testing.T) {
	err := runResolvedOpenCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(0, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD:              common.SSHDConfig{Enabled: true},
			},
		},
		openOptions{VSCode: true, IntelliJ: true},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "--vscode and --intellij cannot be used together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLocalShellSetupScriptUsesPowerShellOnWindows(t *testing.T) {
	result := common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "dev",
		RepoPath:    `C:\Users\john\src\tenant-a`,
		EnvConfig: common.EnvConfig{
			KubernetesContext: "cluster-dev",
		},
	}

	got := localShellSetupScript(result, openNoShellDialectPowerShell)
	want := "kubectl config use-context 'cluster-dev' | Out-Null\n" +
		"kubectl config set-context --current '--namespace=tenant-a-dev' | Out-Null\n" +
		"Set-Location -LiteralPath 'C:\\Users\\john\\src\\tenant-a'\n"
	if got != want {
		t.Fatalf("unexpected PowerShell setup script:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestOpenNoShellHintLinesSuggestAliasAndStartupFileWhenMissing(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	result := common.OpenResult{Tenant: "frs", Environment: "local", Title: "frs-local"}

	lines := openNoShellHintLines(result, "/bin/zsh")

	if len(lines) != 2 {
		t.Fatalf("unexpected hint lines: %+v", lines)
	}
	if lines[0] != "one-liner alias:" {
		t.Fatalf("unexpected intro line: %q", lines[0])
	}
	if lines[1] != `alias frs-local='eval "$(erun open frs local --no-shell)"'` {
		t.Fatalf("unexpected alias line: %q", lines[1])
	}
}

func TestOpenNoShellHintLinesUsePowerShellFunctionOnWindows(t *testing.T) {
	previousHostOS := currentHostOS
	currentHostOS = func() common.HostOS { return common.HostOSWindows }
	t.Cleanup(func() {
		currentHostOS = previousHostOS
	})

	result := common.OpenResult{Tenant: "frs", Environment: "local", Title: "frs-local"}
	lines := openNoShellHintLines(result, "")

	if len(lines) != 2 {
		t.Fatalf("unexpected hint lines: %+v", lines)
	}
	if lines[0] != "one-liner function:" {
		t.Fatalf("unexpected intro line: %q", lines[0])
	}
	if lines[1] != "function frs-local { erun open frs local --no-shell | Invoke-Expression }" {
		t.Fatalf("unexpected function line: %q", lines[1])
	}
}

func TestOpenNoShellHintLinesRecommendAliasWhenConfigured(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	result := common.OpenResult{Tenant: "frs", Environment: "local", Title: "frs-local"}
	startupPath := filepath.Join(homeDir, ".zshrc")
	if err := os.WriteFile(startupPath, []byte(`alias frs-local='eval "$(erun open frs local --no-shell)"'`+"\n"), 0o644); err != nil {
		t.Fatalf("write startup file: %v", err)
	}

	lines := openNoShellHintLines(result, "/bin/zsh")

	if len(lines) != 1 {
		t.Fatalf("unexpected hint lines: %+v", lines)
	}
	if lines[0] != "configured in your shell startup file: open a new shell to use frs-local" {
		t.Fatalf("unexpected recommendation: %q", lines[0])
	}
}

func TestMaybeConfigureOpenNoShellAliasPromptsAndAppendsToStartupFile(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	result := common.OpenResult{Tenant: "frs", Environment: "local", Title: "frs-local"}
	startupPath := filepath.Join(homeDir, ".zshrc")
	stderr := new(bytes.Buffer)

	err := maybeConfigureOpenNoShellAlias(result, func(prompt promptui.Prompt) (string, error) {
		if !prompt.IsConfirm {
			t.Fatalf("expected confirm prompt, got %+v", prompt)
		}
		if prompt.Label != fmt.Sprintf("add frs-local to %s", startupPath) {
			t.Fatalf("unexpected prompt label: %q", prompt.Label)
		}
		return "", nil
	}, "/bin/zsh", stderr)
	if err != nil {
		t.Fatalf("maybeConfigureOpenNoShellAlias failed: %v", err)
	}

	data, err := os.ReadFile(startupPath)
	if err != nil {
		t.Fatalf("read startup file: %v", err)
	}
	if string(data) != "alias frs-local='eval \"$(erun open frs local --no-shell)\"'\n" {
		t.Fatalf("unexpected startup file contents: %q", string(data))
	}
	output := stderr.String()
	if strings.Contains(output, "one-liner alias:") {
		t.Fatalf("did not expect one-liner output after successful add: %q", output)
	}
	if !strings.Contains(output, "added frs-local to "+startupPath) || !strings.Contains(output, "open a new shell to use frs-local") {
		t.Fatalf("unexpected stderr output: %q", output)
	}
}

func TestMaybeConfigureOpenNoShellAliasRecommendsConfiguredAliasWithoutPrompt(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	result := common.OpenResult{Tenant: "frs", Environment: "local", Title: "frs-local"}
	startupPath := filepath.Join(homeDir, ".zshrc")
	if err := os.WriteFile(startupPath, []byte(`alias frs-local='eval "$(erun open frs local --no-shell)"'`+"\n"), 0o644); err != nil {
		t.Fatalf("write startup file: %v", err)
	}
	stderr := new(bytes.Buffer)

	err := maybeConfigureOpenNoShellAlias(result, func(prompt promptui.Prompt) (string, error) {
		t.Fatalf("did not expect prompt when alias is already configured: %+v", prompt)
		return "", nil
	}, "/bin/zsh", stderr)
	if err != nil {
		t.Fatalf("maybeConfigureOpenNoShellAlias failed: %v", err)
	}
	if got := strings.TrimSpace(stderr.String()); got != "configured in your shell startup file: open a new shell to use frs-local" {
		t.Fatalf("unexpected stderr output: %q", got)
	}
}

func TestOpenCommandDryRunPrintsResolvedOpenTraceWithoutLaunchingShell(t *testing.T) {
	repoPath := t.TempDir()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			if req.Name != "tenant-a-devops" || req.Namespace != "tenant-a-dev" || req.KubernetesContext != "cluster-dev" {
				t.Fatalf("unexpected deployment check: %+v", req)
			}
			return true, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "tenant-a", "dev", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("did not expect stdout output during dry-run, got %q", got)
	}
	output := stderr.String()
	for _, want := range []string{
		"kubectl --context cluster-dev --namespace tenant-a-dev wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context cluster-dev --namespace tenant-a-dev exec -it -c tenant-a-devops deployment/tenant-a-devops -- /bin/sh -lc '<bootstrap-script>'",
		"bootstrap-script:",
		"  set -eu",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run trace to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandDryRunPrintsDeployPlanWhenDevopsRuntimeIsMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	if err := os.WriteFile(filepath.Join(chartPath, "values.dev.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.dev.yaml: %v", err)
	}
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   projectRoot,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return false, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           chartPath,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "tenant-a", "dev", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace tenant-a-dev --kube-context cluster-dev -f " + filepath.Join(chartPath, "values.dev.yaml") + " --set-string tenant=tenant-a --set-string environment=dev",
		"kubectl --context cluster-dev --namespace tenant-a-dev wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context cluster-dev --namespace tenant-a-dev exec -it -c tenant-a-devops deployment/tenant-a-devops -- /bin/sh -lc '<bootstrap-script>'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandDryRunRedeploysWhenRuntimeHasLocalBuilds(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	componentName := "tenant-a-devops"
	componentRoot := filepath.Join(projectRoot, componentName)
	chartPath := filepath.Join(componentRoot, "k8s", componentName)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}
	workdir := filepath.Join(componentRoot, "docker", componentName)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   projectRoot,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			t.Fatalf("did not expect deployment check when local runtime builds exist: %+v", req)
			return true, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment execution during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "tenant-a", "local", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"docker buildx build --builder erun-multiarch",
		"--platform 'linux/amd64,linux/arm64'",
		"--build-arg ERUN_VERSION=1.0.0",
		"--push",
		"helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace tenant-a-local --kube-context cluster-dev -f " + filepath.Join(chartPath, "values.local.yaml") + " --set-string tenant=tenant-a --set-string environment=local",
		"kubectl --context cluster-dev --namespace tenant-a-local wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "docker push erunpaas/tenant-a-devops:1.0.0") {
		t.Fatalf("did not expect separate docker push in dry-run output, got %q", output)
	}
}

func TestOpenCommandPersistsSnapshotPreferenceForLocalEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")

	projectRoot := t.TempDir()
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              common.DefaultEnvironment,
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		Store: common.ConfigStore{},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return true, nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", common.DefaultEnvironment, "--snapshot=false"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", common.DefaultEnvironment)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.Snapshot == nil || *envConfig.Snapshot {
		t.Fatalf("expected snapshot preference to be saved as false, got %+v", envConfig)
	}
}

func TestOpenCommandDryRunUsesConfiguredContextForLocalDeploy(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"erun", "rancher-desktop"}, "rancher-desktop")

	projectRoot := t.TempDir()
	componentName := "erun-devops"
	componentRoot := filepath.Join(projectRoot, componentName)
	chartPath := filepath.Join(componentRoot, "k8s", componentName)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}
	workdir := filepath.Join(componentRoot, "docker", componentName)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("erun", common.EnvConfig{
		Name:              common.DefaultEnvironment,
		RepoPath:          projectRoot,
		KubernetesContext: "erun",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: common.ConfigStore{},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			t.Fatalf("did not expect deployment check when local runtime builds exist: %+v", req)
			return true, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment execution during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "erun", common.DefaultEnvironment, "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace erun-local --kube-context erun -f " + filepath.Join(chartPath, "values.local.yaml") + " --set-string tenant=erun --set-string environment=local",
		"kubectl --context erun --namespace erun-local wait --for=condition=Available --timeout 2m0s deployment/erun-devops",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "--context rancher-desktop") || strings.Contains(output, "--kube-context rancher-desktop") {
		t.Fatalf("did not expect deploy trace to use current context, got %q", output)
	}
}

func TestOpenCommandUsesPersistedSnapshotPreferenceForLocalEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")

	projectRoot := t.TempDir()
	componentName := "tenant-a-devops"
	componentRoot := filepath.Join(projectRoot, componentName)
	chartPath := filepath.Join(componentRoot, "k8s", componentName)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}
	workdir := filepath.Join(componentRoot, "docker", componentName)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	snapshot := false
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              common.DefaultEnvironment,
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		Snapshot:          &snapshot,
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	checkedDeployment := false
	cmd := newTestRootCmd(testRootDeps{
		Store: common.ConfigStore{},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			checkedDeployment = true
			if req.Name != "tenant-a-devops" || req.Namespace != "tenant-a-local" || req.KubernetesContext != "cluster-local" {
				t.Fatalf("unexpected deployment check: %+v", req)
			}
			return true, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "tenant-a", common.DefaultEnvironment, "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !checkedDeployment {
		t.Fatal("expected deployment existence check when snapshot preference is disabled")
	}

	output := stderr.String()
	for _, unwanted := range []string{
		"docker build -t",
		"docker push ",
		"helm upgrade --install",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("did not expect %q in dry-run output, got %q", unwanted, output)
		}
	}
	for _, want := range []string{
		"kubectl --context cluster-local --namespace tenant-a-local wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context cluster-local --namespace tenant-a-local exec -it -c tenant-a-devops deployment/tenant-a-devops -- /bin/sh -lc '<bootstrap-script>'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandDryRunFallsBackToDefaultRuntimeChartWhenTenantRepoHasNoDevopsChart(t *testing.T) {
	repoPath := t.TempDir()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "frs"},
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return false, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"open", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if strings.Contains(output, "docker build -t") || strings.Contains(output, "docker push ") {
		t.Fatalf("did not expect local build or push for default runtime chart, got %q", output)
	}
	for _, want := range []string{
		"helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace frs-local --kube-context cluster-dev -f ",
		"kubectl --context cluster-dev --namespace frs-local wait --for=condition=Available --timeout 2m0s deployment/frs-devops",
		"kubectl --context cluster-dev --namespace frs-local exec -it -c frs-devops deployment/frs-devops -- /bin/sh -lc '<bootstrap-script>'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
	for _, want := range []string{
		"--set-string tenant=frs",
		"--set-string environment=local",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandPromptsToCreateMissingRuntimeChartAndUsesCreatedChart(t *testing.T) {
	repoPath := t.TempDir()
	deployed := common.HelmDeployParams{}
	launched := common.ShellLaunchParams{}

	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "frs"},
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if !prompt.IsConfirm {
				t.Fatalf("expected confirm prompt, got %+v", prompt)
			}
			if prompt.Label != fmt.Sprintf("create frs-devops chart in %s", repoPath) {
				t.Fatalf("unexpected prompt label: %q", prompt.Label)
			}
			return "", nil
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return false, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deployed = req
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	chartPath := filepath.Join(repoPath, "frs-devops", "k8s", "frs-devops")
	if deployed.ChartPath != chartPath {
		t.Fatalf("expected created local chart path, got %+v", deployed)
	}
	if deployed.ReleaseName != "frs-devops" {
		t.Fatalf("unexpected release name: %+v", deployed)
	}
	if _, err := os.Stat(filepath.Join(chartPath, "Chart.yaml")); err != nil {
		t.Fatalf("expected generated chart to exist: %v", err)
	}
	if launched.Namespace != "frs-local" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

func TestOpenCommandSkipsLocalRuntimeChartPromptForRemoteRepo(t *testing.T) {
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   "/home/erun/git/erun",
			toolConfig: common.ERunConfig{DefaultTenant: "erun"},
			tenant: &common.TenantConfig{
				Name:               "erun",
				ProjectRoot:        "/home/erun/git/erun",
				DefaultEnvironment: "local",
				Remote:             true,
			},
			env: &common.EnvConfig{
				Name:              "local",
				RepoPath:          "/home/erun/git/erun",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			t.Fatalf("did not expect chart creation prompt for remote repo: %+v", prompt)
			return "", nil
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return true, nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != "/home/erun/git/erun" || launched.Title != "erun-local" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if !launched.RemoteRepo {
		t.Fatalf("expected remote shell launch params, got %+v", launched)
	}
}

func TestOpenCommandRunsManagedDeployAndReattachesWhenShellRequestsHandoff(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	if err := os.WriteFile(filepath.Join(chartPath, "values.dev.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.dev.yaml: %v", err)
	}

	launchCalls := 0
	deployed := common.HelmDeployParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   projectRoot,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{Dir: projectRoot}, nil
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return true, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deployed = req
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launchCalls++
			if launchCalls == 1 {
				return common.ErrShellReattachDeploy
			}
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launchCalls != 2 {
		t.Fatalf("expected shell to relaunch after handoff, got %d launches", launchCalls)
	}
	if deployed.ChartPath != chartPath || deployed.ReleaseName != "erun-devops" {
		t.Fatalf("expected managed deploy before reattach, got %+v", deployed)
	}
}

func TestOpenCommandLaunchesShellWithDefaults(t *testing.T) {
	repoPath := t.TempDir()
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-local" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-local" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestOpenCommandLaunchesShellWithDefaultTenantAndRequestedEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestOpenCommandRunsInitWhenKubernetesContextIsMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "dev",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:     "dev",
		RepoPath: projectRoot,
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		Store: common.ConfigStore{},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			t.Fatalf("unexpected prompt: %+v", prompt)
			return "", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			if fmt.Sprint(prompt.Label) != fmt.Sprintf("Kubernetes context for environment %q in tenant %q", "dev", "tenant-a") {
				t.Fatalf("unexpected select prompt: %+v", prompt)
			}
			return 0, "cluster-dev", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-dev", "cluster-prod"}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			if contextName != "cluster-dev" || namespace != "tenant-a-dev" {
				t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", contextName, namespace)
			}
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("unexpected shell launch: %+v", req)
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.KubernetesContext != "cluster-dev" {
		t.Fatalf("expected kubernetes context to be saved, got %+v", envConfig)
	}
}

func TestOpenCommandRunsInitWhenEnvironmentIsMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "dev",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		Store: common.ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if fmt.Sprint(prompt.Label) == fmt.Sprintf("Container registry for environment %q in tenant %q", "dev", "tenant-a") {
				return "", nil
			}
			return "y", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			if fmt.Sprint(prompt.Label) != fmt.Sprintf("Kubernetes context for environment %q in tenant %q", "dev", "tenant-a") {
				t.Fatalf("unexpected select prompt: %+v", prompt)
			}
			return 0, "cluster-dev", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-dev"}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			if contextName != "cluster-dev" || namespace != "tenant-a-dev" {
				t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", contextName, namespace)
			}
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("unexpected shell launch: %+v", req)
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.KubernetesContext != "cluster-dev" {
		t.Fatalf("expected environment to be initialized, got %+v", envConfig)
	}
}

func TestOpenCommandDeploysDevopsWhenMissing(t *testing.T) {
	repoPath := t.TempDir()
	chartPath := filepath.Join(repoPath, "erun-devops", "k8s", "erun-devops")
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: erun-devops\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.dev.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.dev.yaml: %v", err)
	}

	launched := common.ShellLaunchParams{}
	deployed := common.HelmDeployParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			if req.Name != "tenant-a-devops" || req.Namespace != "tenant-a-dev" || req.KubernetesContext != "cluster-dev" {
				t.Fatalf("unexpected deployment check: %+v", req)
			}
			return false, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deployed = req
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if deployed.ReleaseName != "tenant-a-devops" || deployed.ChartPath != chartPath {
		t.Fatalf("unexpected deploy request: %+v", deployed)
	}
	if deployed.ValuesFilePath != filepath.Join(chartPath, "values.dev.yaml") {
		t.Fatalf("unexpected values path: %+v", deployed)
	}
	if deployed.Namespace != "tenant-a-dev" || deployed.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected deployment target: %+v", deployed)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

type openCommandStore struct {
	repoPath   string
	toolConfig common.ERunConfig
	tenant     *common.TenantConfig
	env        *common.EnvConfig
}

func (s openCommandStore) LoadERunConfig() (common.ERunConfig, string, error) {
	return s.toolConfig, "", nil
}

func (openCommandStore) SaveERunConfig(common.ERunConfig) error {
	return nil
}

func (openCommandStore) ListTenantConfigs() ([]common.TenantConfig, error) {
	return nil, nil
}

func (s openCommandStore) LoadTenantConfig(tenant string) (common.TenantConfig, string, error) {
	if s.tenant != nil {
		config := *s.tenant
		if config.Name == "" {
			config.Name = tenant
		}
		return config, "", nil
	}
	return common.TenantConfig{
		Name:               tenant,
		ProjectRoot:        s.repoPath,
		DefaultEnvironment: "local",
	}, "", nil
}

func (openCommandStore) SaveTenantConfig(common.TenantConfig) error {
	return nil
}

func (s openCommandStore) LoadEnvConfig(tenant, env string) (common.EnvConfig, string, error) {
	if s.env != nil {
		config := *s.env
		if config.Name == "" {
			config.Name = env
		}
		return config, "", nil
	}
	return common.EnvConfig{
		Name:              env,
		RepoPath:          s.repoPath,
		KubernetesContext: "cluster-dev",
	}, "", nil
}

func (openCommandStore) SaveEnvConfig(string, common.EnvConfig) error {
	return nil
}
