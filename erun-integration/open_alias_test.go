package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

const zshAliasLine = `alias team-dev='eval "$(erun open team dev --no-shell)"'`

func testOpenAliasPromptDryRunAcceptTracesAppend(t *testing.T) {
	t.Parallel()
	// ERUN_FORCE_TTY=1 lifts the TTY gate in the piped harness; SHELL pins ~/.zshrc.
	// The alias confirm must be the only prompt, since readline read-ahead would
	// starve a second one.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	zshrc := filepath.Join(setup.Home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# seeded zshrc\n"), 0o644); err != nil {
		t.Fatalf("seed ~/.zshrc: %v", err)
	}
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
	golden.Equal(t, "open/alias_prompt_dry_run_accept_traces_append", normalize.PromptConfirm(result.Combined))
	body, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read ~/.zshrc: %v", err)
	}
	if string(body) != "# seeded zshrc\n" {
		t.Errorf("dry-run must leave ~/.zshrc untouched, got:\n%s", body)
	}
}

func testOpenAliasPromptDeclinePrintsHint(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	zshrc := filepath.Join(setup.Home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# seeded zshrc\n"), 0o644); err != nil {
		t.Fatalf("seed ~/.zshrc: %v", err)
	}
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "n\n"})
	golden.Equal(t, "open/alias_prompt_decline_prints_hint", normalize.PromptConfirm(result.Combined))
	body, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read ~/.zshrc: %v", err)
	}
	if string(body) != "# seeded zshrc\n" {
		t.Errorf("declined prompt must leave ~/.zshrc untouched, got:\n%s", body)
	}
}

func testOpenAliasPromptAcceptAppendsAliasRealRun(t *testing.T) {
	// SeedDevopsRepo keeps the "create team-devops chart?" prompt from firing.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	zshrc := filepath.Join(setup.Home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# seeded zshrc\n"), 0o644); err != nil {
		t.Fatalf("seed ~/.zshrc: %v", err)
	}
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/cwd",
		MCPPort:        26100,
		SSHPort:        26122,
	})...)
	envVars = append(envVars, "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/alias_prompt_accept_appends_alias_real_run", normalize.PromptConfirm(result.Combined))
	body, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("read ~/.zshrc: %v", err)
	}
	if want := "# seeded zshrc\n" + zshAliasLine + "\n"; string(body) != want {
		t.Errorf("expected ~/.zshrc to gain the alias line, want:\n%s\ngot:\n%s", want, body)
	}
}

func testOpenAliasPromptSkippedWhenAliasConfigured(t *testing.T) {
	t.Parallel()
	// No stdin is wired, so a leaked prompt would hang and trip the timeout.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	zshrc := filepath.Join(setup.Home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte(zshAliasLine+"\n"), 0o644); err != nil {
		t.Fatalf("seed ~/.zshrc: %v", err)
	}
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/alias_prompt_skipped_when_alias_configured", normalize.Apply(result.Combined))
}

func testOpenAliasPromptBashAcceptCreatesBashrc(t *testing.T) {
	// None of the bash candidates exist, so ~/.bashrc is created.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/cwd",
		MCPPort:        26100,
		SSHPort:        26122,
	})...)
	envVars = append(envVars, "ERUN_FORCE_TTY=1", "SHELL=/bin/bash")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/alias_prompt_bash_accept_creates_bashrc", normalize.PromptConfirm(result.Combined))
	body, err := os.ReadFile(filepath.Join(setup.Home, ".bashrc"))
	if err != nil {
		t.Fatalf("read ~/.bashrc (append must create the missing file): %v", err)
	}
	if want := zshAliasLine + "\n"; string(body) != want {
		t.Errorf("expected fresh ~/.bashrc with the alias line, want:\n%s\ngot:\n%s", want, body)
	}
}

func testOpenAliasPowershellDialectPrintsFunctionHint(t *testing.T) {
	t.Parallel()
	// PowerShell has no startup file to offer, so only hint lines print.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/pwsh")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/alias_powershell_dialect_prints_function_hint", normalize.Apply(result.Combined))
}
