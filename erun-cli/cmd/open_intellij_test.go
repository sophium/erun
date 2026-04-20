package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	common "github.com/sophium/erun/erun-common"
)

func TestLaunchIntelliJEnsuresKnownHostBeforeOpening(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevOpenInstalled := openInstalledIntelliJAppFunc
	prevRegister := registerIntelliJProjectFunc
	prevHostOS := currentHostOS
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		openInstalledIntelliJAppFunc = prevOpenInstalled
		registerIntelliJProjectFunc = prevRegister
		currentHostOS = prevHostOS
	})

	callOrder := make([]string, 0, 2)
	currentHostOS = func() common.HostOS { return common.HostOSDarwin }
	ensureLocalSSHDKnownHostFunc = func(_ common.Context, result common.OpenResult) error {
		callOrder = append(callOrder, "known_hosts")
		if result.Tenant != "tenant-a" {
			t.Fatalf("unexpected target: %+v", result)
		}
		return nil
	}
	registerIntelliJProjectFunc = func(_ common.Context, result common.OpenResult, hostOS common.HostOS) error {
		callOrder = append(callOrder, "register_project")
		if hostOS != common.HostOSDarwin {
			t.Fatalf("unexpected host OS: %s", hostOS)
		}
		if result.Tenant != "tenant-a" {
			t.Fatalf("unexpected target: %+v", result)
		}
		return nil
	}
	openInstalledIntelliJAppFunc = func(_ common.Context, result common.OpenResult, hostOS common.HostOS) error {
		callOrder = append(callOrder, "open_intellij")
		if hostOS != common.HostOSDarwin {
			t.Fatalf("unexpected host OS: %s", hostOS)
		}
		if result.Tenant != "tenant-a" {
			t.Fatalf("unexpected target: %+v", result)
		}
		return nil
	}

	stderr := new(bytes.Buffer)
	err := launchIntelliJ(common.Context{Stderr: stderr}, common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "remote",
		TenantConfig: common.TenantConfig{
			Name:   "tenant-a",
			Remote: true,
		},
		EnvConfig: common.EnvConfig{
			Name:     "remote",
			Remote:   true,
			RepoPath: "/home/erun/git/tenant-a",
			SSHD: common.SSHDConfig{
				Enabled:       true,
				LocalPort:     62222,
				PublicKeyPath: "/Users/test/.ssh/id_ed25519.pub",
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	}, nil)
	if err != nil {
		t.Fatalf("launchIntelliJ failed: %v", err)
	}
	if got := strings.Join(callOrder, ","); got != "known_hosts,register_project,open_intellij" {
		t.Fatalf("unexpected call order: %s", got)
	}
	for _, want := range []string{
		"Opened IntelliJ IDEA locally.",
		"Remote Development -> SSH",
		"host: erun-tenant-a-remote",
		"user: erun",
		"key: /Users/test/.ssh/id_ed25519",
		"project: /home/erun/git/tenant-a",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected guidance to contain %q, got:\n%s", want, stderr.String())
		}
	}
}

func TestLaunchIntelliJReturnsKnownHostError(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevOpenInstalled := openInstalledIntelliJAppFunc
	prevRegister := registerIntelliJProjectFunc
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		openInstalledIntelliJAppFunc = prevOpenInstalled
		registerIntelliJProjectFunc = prevRegister
	})

	ensureLocalSSHDKnownHostFunc = func(common.Context, common.OpenResult) error {
		return errors.New("known host failed")
	}
	openInstalledIntelliJAppFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		t.Fatal("did not expect IntelliJ launch when known_hosts update fails")
		return nil
	}
	registerIntelliJProjectFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		t.Fatal("did not expect JetBrains config registration when known_hosts update fails")
		return nil
	}

	err := launchIntelliJ(common.Context{}, common.OpenResult{}, nil)
	if err == nil || err.Error() != "known host failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLaunchIntelliJReturnsInstalledIDEAError(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevOpenInstalled := openInstalledIntelliJAppFunc
	prevRegister := registerIntelliJProjectFunc
	prevHostOS := currentHostOS
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		openInstalledIntelliJAppFunc = prevOpenInstalled
		registerIntelliJProjectFunc = prevRegister
		currentHostOS = prevHostOS
	})

	currentHostOS = func() common.HostOS { return common.HostOSDarwin }
	ensureLocalSSHDKnownHostFunc = func(common.Context, common.OpenResult) error { return nil }
	registerIntelliJProjectFunc = func(common.Context, common.OpenResult, common.HostOS) error { return nil }
	openInstalledIntelliJAppFunc = func(common.Context, common.OpenResult, common.HostOS) error {
		return errors.New("IntelliJ IDEA is not available on this host")
	}

	err := launchIntelliJ(common.Context{}, common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "remote",
		TenantConfig: common.TenantConfig{
			Name:   "tenant-a",
			Remote: true,
		},
		EnvConfig: common.EnvConfig{
			Name:     "remote",
			Remote:   true,
			RepoPath: "/home/erun/git/tenant-a",
			SSHD: common.SSHDConfig{
				Enabled:   true,
				LocalPort: 62222,
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	}, nil)
	if err == nil {
		t.Fatal("expected missing IntelliJ IDEA error")
	}
	for _, want := range []string{
		"IntelliJ IDEA launcher failed on macOS.",
		`host "erun-tenant-a-remote"`,
		"IntelliJ IDEA is not available on this host",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, err.Error())
		}
	}
}

func TestOpenInstalledIntelliJAppDryRunTracesLaunch(t *testing.T) {
	prevExec := ideExecCommand
	t.Cleanup(func() {
		ideExecCommand = prevExec
	})

	ideExecCommand = func(name string, args ...string) *exec.Cmd {
		t.Fatal("did not expect process execution during dry-run")
		return nil
	}

	stderr := new(bytes.Buffer)
	err := openInstalledIntelliJApp(common.Context{
		Logger: common.NewLoggerWithWriters(2, stderr, stderr),
		Stderr: stderr,
		DryRun: true,
	}, common.OpenResult{}, common.HostOSDarwin)
	if err != nil {
		t.Fatalf("openInstalledIntelliJApp failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "open -a 'IntelliJ IDEA'") {
		t.Fatalf("expected dry-run trace to contain IntelliJ launch, got:\n%s", stderr.String())
	}
}

func TestRegisterIntelliJProjectDryRunTracesOptionsDir(t *testing.T) {
	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	prevStat := ideStat
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
		ideStat = prevStat
	})

	optionsDir := t.TempDir()
	ideUserHomeDir = func() (string, error) { return "/Users/test", nil }
	ideGlob = func(pattern string) ([]string, error) { return []string{optionsDir}, nil }
	ideStat = os.Stat

	stderr := new(bytes.Buffer)
	err := registerIntelliJProject(common.Context{
		Logger: common.NewLoggerWithWriters(2, stderr, stderr),
		Stderr: stderr,
		DryRun: true,
	}, common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "remote",
		TenantConfig: common.TenantConfig{
			Name:   "tenant-a",
			Remote: true,
		},
		EnvConfig: common.EnvConfig{
			Name:     "remote",
			Remote:   true,
			RepoPath: "/home/erun/git/tenant-a",
			SSHD: common.SSHDConfig{
				Enabled:       true,
				LocalPort:     62222,
				PublicKeyPath: "/Users/test/.ssh/id_ed25519.pub",
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	}, common.HostOSDarwin)
	if err != nil {
		t.Fatalf("registerIntelliJProject failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "write IntelliJ SSH project config in "+optionsDir) {
		t.Fatalf("expected dry-run trace to contain options dir, got:\n%s", stderr.String())
	}
}

func TestResolveIntelliJOptionsDirChoosesMostRecentlyModified(t *testing.T) {
	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	prevStat := ideStat
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
		ideStat = prevStat
	})

	root := t.TempDir()
	oldDir := filepath.Join(root, "IntelliJIdea2025.2", "options")
	newDir := filepath.Join(root, "IntelliJIdea2025.3", "options")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("mkdir oldDir: %v", err)
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatalf("mkdir newDir: %v", err)
	}
	oldTime := time.Unix(100, 0)
	newTime := time.Unix(200, 0)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes oldDir: %v", err)
	}
	if err := os.Chtimes(newDir, newTime, newTime); err != nil {
		t.Fatalf("chtimes newDir: %v", err)
	}

	ideUserHomeDir = func() (string, error) { return "/Users/test", nil }
	ideGlob = func(pattern string) ([]string, error) { return []string{oldDir, newDir}, nil }
	ideStat = os.Stat

	got, err := resolveIntelliJOptionsDir(common.HostOSDarwin)
	if err != nil {
		t.Fatalf("resolveIntelliJOptionsDir failed: %v", err)
	}
	if got != newDir {
		t.Fatalf("unexpected options dir: %q", got)
	}
}

func TestResolveIntelliJOptionsDirWindowsUsesAppData(t *testing.T) {
	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	prevStat := ideStat
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
		ideStat = prevStat
	})

	root := t.TempDir()
	optionsDir := filepath.Join(root, "JetBrains", "IntelliJIdea2025.3", "options")
	if err := os.MkdirAll(optionsDir, 0o700); err != nil {
		t.Fatalf("mkdir optionsDir: %v", err)
	}
	t.Setenv("APPDATA", root)

	ideUserHomeDir = func() (string, error) { return "/Users/ignored", nil }
	ideGlob = func(pattern string) ([]string, error) { return []string{optionsDir}, nil }
	ideStat = os.Stat

	got, err := resolveIntelliJOptionsDir(common.HostOSWindows)
	if err != nil {
		t.Fatalf("resolveIntelliJOptionsDir failed: %v", err)
	}
	if got != optionsDir {
		t.Fatalf("unexpected options dir: %q", got)
	}
}
