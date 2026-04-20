package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHHostAlias(t *testing.T) {
	if got := SSHHostAlias("Tenant_A", "Remote Dev"); got != "erun-tenant-a-remote-dev" {
		t.Fatalf("unexpected SSH host alias: %q", got)
	}
}

func TestSSHPrivateKeyPath(t *testing.T) {
	if got := SSHPrivateKeyPath("/tmp/id_ed25519.pub"); got != "/tmp/id_ed25519" {
		t.Fatalf("unexpected private key path: %q", got)
	}
	if got := SSHPrivateKeyPath("/tmp/custom-key"); got != "" {
		t.Fatalf("expected empty private key path, got %q", got)
	}
}

func TestRuntimeDockerfileUnlocksSSHUserAccount(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read runtime Dockerfile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "passwd -d erun") {
		t.Fatalf("expected runtime Dockerfile to unlock erun account for SSH public-key auth, got:\n%s", content)
	}
}

func TestRuntimeDockerfileUsesDesktopGoVersion(t *testing.T) {
	dockerfileData, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read runtime Dockerfile: %v", err)
	}
	goModData, err := os.ReadFile(filepath.Join("..", "erun-ui", "go.mod"))
	if err != nil {
		t.Fatalf("read erun-ui go.mod: %v", err)
	}

	version, err := moduleGoVersion(string(goModData))
	if err != nil {
		t.Fatalf("resolve erun-ui Go version: %v", err)
	}
	expected := "FROM golang:" + version + " AS builder"
	if !strings.Contains(string(dockerfileData), expected) {
		t.Fatalf("expected runtime Dockerfile to use desktop Go version %q, got:\n%s", expected, string(dockerfileData))
	}
}

func TestRuntimeEntrypointDisablesStrictModesForPVCBackedHome(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read runtime entrypoint: %v", err)
	}
	if !strings.Contains(string(data), "StrictModes no") {
		t.Fatalf("expected runtime entrypoint to disable sshd strict modes for PVC-backed home directory, got:\n%s", string(data))
	}
}

func moduleGoVersion(goMod string) (string, error) {
	for _, line := range strings.Split(goMod, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "go ") {
			continue
		}
		version := strings.TrimSpace(strings.TrimPrefix(line, "go "))
		if version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("go directive not found")
}
