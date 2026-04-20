package cmd

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestVSCodeRemoteFolderURI(t *testing.T) {
	got := vscodeRemoteFolderURI(common.OpenResult{
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
	})
	want := "vscode://vscode-remote/ssh-remote+erun-tenant-a-remote/home/erun/git/tenant-a"
	if got != want {
		t.Fatalf("unexpected VS Code URI:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestVSCodeLaunchCommand(t *testing.T) {
	tests := []struct {
		name    string
		hostOS  common.HostOS
		command string
		args    []string
	}{
		{name: "darwin", hostOS: common.HostOSDarwin, command: "open", args: []string{"vscode://example"}},
		{name: "linux", hostOS: common.HostOSLinux, command: "xdg-open", args: []string{"vscode://example"}},
		{name: "windows", hostOS: common.HostOSWindows, command: "cmd", args: []string{"/c", "start", "", "vscode://example"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, args, err := ideLaunchCommand(tt.hostOS, "vscode://example")
			if err != nil {
				t.Fatalf("ideLaunchCommand failed: %v", err)
			}
			if command != tt.command {
				t.Fatalf("unexpected command: %q", command)
			}
			if len(args) != len(tt.args) {
				t.Fatalf("unexpected args: %+v", args)
			}
			for i := range args {
				if args[i] != tt.args[i] {
					t.Fatalf("unexpected args: %+v", args)
				}
			}
		})
	}
}

func TestLaunchVSCodeEnsuresKnownHostBeforeOpening(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevExec := ideExecCommand
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		ideExecCommand = prevExec
	})

	callOrder := make([]string, 0, 2)
	ensureLocalSSHDKnownHostFunc = func(_ common.Context, result common.OpenResult) error {
		callOrder = append(callOrder, "known_hosts")
		if result.Tenant != "tenant-a" {
			t.Fatalf("unexpected target: %+v", result)
		}
		return nil
	}
	ideExecCommand = func(name string, args ...string) *exec.Cmd {
		callOrder = append(callOrder, "launch")
		if name != "open" {
			t.Fatalf("unexpected command: %q", name)
		}
		return exec.Command("true")
	}

	err := launchVSCode(common.Context{}, common.OpenResult{
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
	})
	if err != nil {
		t.Fatalf("launchVSCode failed: %v", err)
	}
	if got := strings.Join(callOrder, ","); got != "known_hosts,launch" {
		t.Fatalf("unexpected call order: %s", got)
	}
}

func TestLaunchVSCodeReturnsKnownHostError(t *testing.T) {
	prevEnsure := ensureLocalSSHDKnownHostFunc
	prevExec := ideExecCommand
	t.Cleanup(func() {
		ensureLocalSSHDKnownHostFunc = prevEnsure
		ideExecCommand = prevExec
	})

	ensureLocalSSHDKnownHostFunc = func(common.Context, common.OpenResult) error {
		return errors.New("known host failed")
	}
	ideExecCommand = func(name string, args ...string) *exec.Cmd {
		t.Fatal("did not expect VS Code launch when known_hosts update fails")
		return nil
	}

	err := launchVSCode(common.Context{}, common.OpenResult{})
	if err == nil || err.Error() != "known host failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureLocalSSHDKnownHostDryRunTracesKeyscan(t *testing.T) {
	stderr := new(bytes.Buffer)
	err := ensureLocalSSHDKnownHost(common.Context{
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
				Enabled:   true,
				LocalPort: 62222,
			},
		},
		RepoPath: "/home/erun/git/tenant-a",
	})
	if err != nil {
		t.Fatalf("ensureLocalSSHDKnownHost failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "ssh-keyscan -p 62222 127.0.0.1") {
		t.Fatalf("expected dry-run trace to contain ssh-keyscan, got:\n%s", stderr.String())
	}
}
