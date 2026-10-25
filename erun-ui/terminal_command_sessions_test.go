package main

import (
	"context"
	"os"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestStartDeploySessionInjectsMCPAuthPublicKey(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	identityDir := t.TempDir()
	app.identity = newDesktopIdentity(identityDir)
	defer app.shutdown(context.Background())

	if _, err := app.StartDeploySession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	wantPath := mcpauth.DesktopIdentityPublicKeyPath(identityDir)
	written := sessions[0].WrittenString()
	// Use the production builder: prefix, quoting and line ending are per platform.
	want := terminal.BuildLocalErunCommand("/tmp/erun", []string{"deploy", "erun", "remote", "--version", "1.0.19", "--mcp-auth-public-key", wantPath})
	if !strings.Contains(written, want) {
		t.Fatalf("expected deploy command to carry %q, got %q", want, written)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected desktop public key written to %q: %v", wantPath, err)
	}
}

func TestStartInitSessionInjectsMCPAuthPublicKey(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	identityDir := t.TempDir()
	app.identity = newDesktopIdentity(identityDir)
	defer app.shutdown(context.Background())

	if _, err := app.StartInitSession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.19", NoGit: true}, 80, 24); err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	wantPath := mcpauth.DesktopIdentityPublicKeyPath(identityDir)
	wantFlag := "--mcp-auth-public-key " + terminal.ShellQuoteIfNeeded(wantPath)
	written := sessions[0].WrittenString()
	if !strings.Contains(written, wantFlag) {
		t.Fatalf("expected init command to carry %q, got %q", wantFlag, written)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected desktop public key written to %q: %v", wantPath, err)
	}
}
