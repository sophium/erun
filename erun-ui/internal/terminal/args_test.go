package terminal

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestBuildOpenCommandQuotesExecutableAndArgs(t *testing.T) {
	got := buildOpenCommand("/Applications/ERun App/erun", "tenant a", "prod")
	want := "'/Applications/ERun App/erun' open 'tenant a' 'prod'"
	if runtime.GOOS == "windows" {
		want = "& " + want
	}
	if got != want {
		t.Fatalf("unexpected open command: got %q want %q", got, want)
	}
}

func TestBuildOpenArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := BuildOpenArgs(" erun ", " local ")
	want := []string{"open", "erun", "local"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildOpenIDEArgsAddsIDEFlag(t *testing.T) {
	got := buildOpenIDEArgs(uimodel.Selection{Tenant: " erun ", Environment: " remote "}, "vscode")
	want := []string{"open", "erun", "remote", "--vscode"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected VS Code args: got %+v want %+v", got, want)
	}

	got = buildOpenIDEArgs(uimodel.Selection{Tenant: "erun", Environment: "remote"}, "intellij")
	want = []string{"open", "erun", "remote", "--intellij"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected IntelliJ args: got %+v want %+v", got, want)
	}
}

func TestBuildDoctorArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildDoctorArgs(uimodel.Selection{Tenant: " erun ", Environment: " remote "})
	want := []string{"doctor", "erun", "remote"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected doctor args: got %+v want %+v", got, want)
	}
}

func TestBuildOpenNoShellArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildOpenNoShellArgs(" erun ", " local ")
	want := []string{"open", "erun", "local", "--no-shell", "--no-alias-prompt", "--reconnect"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildInitArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildInitArgs(uimodel.Selection{Tenant: " erun ", Environment: " remote "})
	want := []string{"init", "erun", "remote", "--type=remote-agent", "--set-default-tenant=false", "--confirm-environment=true"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildInitArgsIncludesRuntimeVersion(t *testing.T) {
	got := buildInitArgs(uimodel.Selection{
		Tenant:            " erun ",
		Environment:       " remote ",
		Version:           " 1.0.19 ",
		RuntimeImage:      " erun-devops ",
		RuntimeCPU:        " 6 ",
		RuntimeMemory:     " 12Gi ",
		KubernetesContext: " orbstack ",
		ContainerRegistry: " erunpaas ",
		NoGit:             true,
		SetDefaultTenant:  true,
	})
	want := []string{"init", "erun", "remote", "--type=remote-agent", "--version", "1.0.19", "--runtime-image", "erun-devops", "--runtime-cpu", "6", "--runtime-memory", "12Gi", "--kubernetes-context", "orbstack", "--container-registry", "erunpaas", "--set-default-tenant=true", "--confirm-environment=true", "--no-git"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildInitArgsClusterRegistryReplacesContainerRegistry(t *testing.T) {
	got := buildInitArgs(uimodel.Selection{
		Tenant:            "erun",
		Environment:       "remote",
		KubernetesContext: "erun-k3s",
		// ClusterRegistry wins over ContainerRegistry.
		ContainerRegistry: "erunpaas",
		ClusterRegistry:   true,
		SetDefaultTenant:  true,
	})
	want := []string{"init", "erun", "remote", "--type=remote-agent", "--kubernetes-context", "erun-k3s", "--cluster-registry", "--set-default-tenant=true", "--confirm-environment=true"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildLocalErunCommandForOSWindowsUsesPowerShell(t *testing.T) {
	// Without the call operator and CR, PSReadLine leaves the line at ">>".
	got := buildLocalErunCommandForOS("windows", `C:\erun-cli\bin\erun.exe`, []string{"init", "team", "dev", "--type=remote-agent"})
	want := "& 'C:\\erun-cli\\bin\\erun.exe' init team dev --type=remote-agent\r"
	if got != want {
		t.Fatalf("windows command:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildLocalErunCommandForOSUnixUsesPosix(t *testing.T) {
	got := buildLocalErunCommandForOS("linux", "/usr/local/bin/erun", []string{"init", "team", "dev"})
	want := "/usr/local/bin/erun init team dev\n"
	if got != want {
		t.Fatalf("unix command:\n got: %q\nwant: %q", got, want)
	}
}

func TestResolveLocalShellCommandIgnoresPosixShellOnWindows(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/bash")
	shell, args := LocalShellCommand("windows")
	if shell == "/usr/bin/bash" {
		t.Fatalf("windows shell honored POSIX $SHELL: %q", shell)
	}
	lower := strings.ToLower(shell)
	if !strings.Contains(lower, "powershell") && !strings.Contains(lower, "pwsh") {
		t.Fatalf("windows shell = %q, want a powershell/pwsh command", shell)
	}
	if len(args) == 0 || args[0] != "-NoLogo" {
		t.Fatalf("windows shell args = %v, want -NoLogo", args)
	}
	if got, _ := LocalShellCommand("linux"); got != "/usr/bin/bash" {
		t.Fatalf("unix shell = %q, want /usr/bin/bash from $SHELL", got)
	}
}

func TestBuildInitArgsRespectsExplicitType(t *testing.T) {
	got := buildInitArgs(uimodel.Selection{
		Tenant:        "erun",
		Environment:   "local",
		Type:          "local-agent",
		LocalRepoPath: "/Users/me/code/project",
	})
	want := []string{"init", "erun", "local", "--type=local-agent", "--project-root", "/Users/me/code/project", "--set-default-tenant=false", "--confirm-environment=true"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildInitArgsRuntimeTypeOmitsProjectRoot(t *testing.T) {
	got := buildInitArgs(uimodel.Selection{
		Tenant:        "erun",
		Environment:   "prod",
		Type:          "runtime",
		LocalRepoPath: "/should/be/ignored",
	})
	want := []string{"init", "erun", "prod", "--type=runtime", "--set-default-tenant=false", "--confirm-environment=true"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildDeployArgsRunsDeploy(t *testing.T) {
	got := buildDeployArgs(uimodel.Selection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 "})
	want := []string{"deploy", "erun", "remote", "--version", "1.0.19"}
	assertArgsEqual(t, got, want)
}

func TestBuildDeployArgsThreadsRuntimeImageOverride(t *testing.T) {
	got := buildDeployArgs(uimodel.Selection{
		Tenant:       "frs",
		Environment:  "local",
		Version:      "1.0.102",
		RuntimeImage: "ghcr.io/sophium/erun-devops",
	})
	want := []string{"deploy", "frs", "local", "--version", "1.0.102", "--runtime-image", "ghcr.io/sophium/erun-devops"}
	assertArgsEqual(t, got, want)
}

func TestBuildDeployArgsOmitsOwnTenantImage(t *testing.T) {
	got := buildDeployArgs(uimodel.Selection{
		Tenant:       "frs",
		Environment:  "local",
		Version:      "1.0.102",
		RuntimeImage: "ghcr.io/sophium/frs-devops",
	})
	want := []string{"deploy", "frs", "local", "--version", "1.0.102"}
	assertArgsEqual(t, got, want)
}

func assertArgsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestResolveCLIExecutableFromDarwinBundleUsesSiblingCLI(t *testing.T) {
	root := t.TempDir()
	cliPath := filepath.Join(root, "erun")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	appExecutable := filepath.Join(root, "ERun.app", "Contents", "MacOS", "erun-app")
	got := resolveCLIExecutableFromPath("darwin", appExecutable, "erun")
	if got != cliPath {
		t.Fatalf("resolveCLIExecutableFromPath() = %q, want %q", got, cliPath)
	}
}

func TestResolveCLIExecutableFromPathUsesExecutableSibling(t *testing.T) {
	root := t.TempDir()
	cliPath := filepath.Join(root, "erun")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	got := resolveCLIExecutableFromPath("linux", filepath.Join(root, "erun-app"), "erun")
	if got != cliPath {
		t.Fatalf("resolveCLIExecutableFromPath() = %q, want %q", got, cliPath)
	}
}

func TestResolveCLIExecutableHonorsAppCLISeam(t *testing.T) {
	t.Setenv("ERUN_APP_CLI", "/seam/stub/erun")
	if got := ResolveCLIExecutable(); got != "/seam/stub/erun" {
		t.Fatalf("ResolveCLIExecutable() = %q, want the ERUN_APP_CLI override", got)
	}
}

func TestResolveTerminalStartDirUsesExistingPreferredDirectory(t *testing.T) {
	preferred := t.TempDir()

	if got := ResolveStartDir(preferred); got != preferred {
		t.Fatalf("ResolveStartDir(%q) = %q, want %q", preferred, got, preferred)
	}
}

func TestResolveTerminalStartDirFallsBackToWorkingDirectory(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	workingDir := t.TempDir()
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory failed: %v", err)
		}
	}()

	missingDir := filepath.Join(workingDir, "missing")
	got := ResolveStartDir(missingDir)
	gotEval, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) failed: %v", got, err)
	}
	wantEval, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) failed: %v", workingDir, err)
	}
	if gotEval != wantEval {
		t.Fatalf("ResolveStartDir(%q) = %q (%q), want %q (%q)", missingDir, got, gotEval, workingDir, wantEval)
	}
}
