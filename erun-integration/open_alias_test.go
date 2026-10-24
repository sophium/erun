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

// zshAliasLine is the exact alias production appends for team/dev under
// zsh; the tests assert the startup file gains this line verbatim.
const zshAliasLine = `alias team-dev='eval "$(erun open team dev --no-shell)"'`

func testOpenAliasPromptDryRunAcceptTracesAppend(t *testing.T) {
	t.Parallel()
	// ERUN_FORCE_TTY=1 lifts the stdout-TTY gate so the alias-setup flow
	// runs in the piped harness; SHELL=/bin/zsh pins the startup file to
	// ~/.zshrc. Accepting in --dry-run must trace the append but leave
	// ~/.zshrc untouched. The alias confirm must stay the run's only
	// prompt — readline read-ahead would starve a second one.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	zshrc := filepath.Join(setup.Home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# seeded zshrc\n"), 0o644); err != nil {
		t.Fatalf("seed ~/.zshrc: %v", err)
	}
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
	golden.Equal(t, "open/alias_prompt_dry_run_accept_traces_append", normalize.PromptConfirm(result.Combined))
	// Dry-run must not mutate the startup file (side effect outside the
	// captured streams).
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
	// Declining ("n") must print the alias hint and write no file.
	// ERUN_FORCE_TTY=1 lifts the TTY gate; SHELL=/bin/zsh pins ~/.zshrc.
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
	// Real-run: accepting the confirm must append the alias line to
	// ~/.zshrc. The kubectl stub reports deployed-and-matching so no helm
	// deploy runs. SeedDevopsRepo keeps the runtime spec off the default
	// chart path so the "create team-devops chart?" prompt never fires —
	// the alias confirm must stay the subprocess's single prompt
	// (readline read-ahead).
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
	// The append is a side effect outside the captured streams: the
	// seeded content must survive and the alias line must follow it.
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
	// When ~/.zshrc already carries the team-dev alias,
	// detectOpenNoShellAliasStartupFile reports it configured
	// (startupFileHasAlias true branch) and the whole prompt is skipped:
	// no confirm, no hint lines, just the setup script. No stdin is
	// wired, so a leaked prompt would hang and trip the run timeout.
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
	// SHELL=/bin/bash exercises openNoShellStartupFiles' bash arm: the
	// candidate list is ~/.bashrc, ~/.bash_profile, ~/.profile, none of
	// which exist, so detectOpenNoShellAliasStartupFile falls through
	// every stat error and offers the preferred ~/.bashrc. Accepting in
	// real-run drives appendOpenNoShellAlias's create-missing-file arm.
	// Real-run shape mirrors alias_prompt_accept_appends_alias_real_run
	// (deployed kubectl stub + port-forward sim on the 26100 range).
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
	// SHELL=/bin/pwsh resolves the PowerShell dialect:
	// detectOpenNoShellAliasStartupFile refuses a startup file, so the
	// flow takes the hint-lines-only arm — "one-liner function:" plus the
	// Invoke-Expression wrapper — and the stdout setup script switches to
	// the PowerShell form (powerShellQuote). No prompt fires, so no
	// stdin is wired.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/pwsh")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/alias_powershell_dialect_prints_function_hint", normalize.Apply(result.Combined))
}
