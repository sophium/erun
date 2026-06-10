package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestListRemoteAppSessionsParsesPodSockets drives the read-model through a
// PATH-stubbed kubectl that prints a pod's /tmp/erun-app listing: dtach
// sockets for this env become session ids, while owner files and other envs'
// sockets are ignored. This is what lets a fresh ERun window rebuild tabs for
// sessions another window created.
func TestListRemoteAppSessionsParsesPodSockets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-stub kubectl uses a shell script; skipping on Windows")
	}
	stubDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf 'erun-remote-open-1.dtach\\nerun-remote-ai.dtach\\nerun-remote-open-1.owner\\nother-env-open-2.dtach\\n'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	app := NewApp(erunUIDeps{store: stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: t.TempDir(), DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", KubernetesContext: "ctx"},
		},
	}})

	got := app.ListRemoteAppSessions(uiSelection{Tenant: "erun", Environment: "remote"})
	want := []string{"open-1", "ai"}
	if !slices.Equal(got, want) {
		t.Fatalf("ListRemoteAppSessions = %v, want %v", got, want)
	}
}

// TestListRemoteAppSessionsFailsSoft pins the contract that detection never
// surfaces an error into the open flow: an unknown env and an env without a
// kubernetes context both yield nil.
func TestListRemoteAppSessionsFailsSoft(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: t.TempDir(), DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {Name: "local"}, // no kubernetes context
		},
	}})

	if got := app.ListRemoteAppSessions(uiSelection{Tenant: "erun", Environment: "local"}); got != nil {
		t.Fatalf("env without kubernetes context must yield nil, got %v", got)
	}
	if got := app.ListRemoteAppSessions(uiSelection{Tenant: "erun", Environment: "missing"}); got != nil {
		t.Fatalf("unknown env must yield nil, got %v", got)
	}
}
