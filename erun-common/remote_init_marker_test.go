package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRemoteInitMarkerIgnoresOtherTenants(t *testing.T) {
	home := t.TempDir()
	otherDir := filepath.Join(home, ".erun", "other", "dev")
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	otherBody := "tenant: other\nenvironment: dev\nproject_root: /repo\nbootstrap_complete: true\n"
	if err := os.WriteFile(filepath.Join(otherDir, "bootstrap.yaml"), []byte(otherBody), 0o600); err != nil {
		t.Fatalf("write other: %v", err)
	}

	_, found, err := LoadRemoteInitMarker(home, "team", "dev")
	if err != nil {
		t.Fatalf("LoadRemoteInitMarker failed: %v", err)
	}
	if found {
		t.Fatalf("expected no marker for tenant 'team' when only tenant 'other' has one")
	}
}

func TestLoadRemoteInitMarkerLegacyFallbackOnMatchingTenantEnv(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".erun"), 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacy := "tenant: team\nenvironment: dev\nproject_root: /repo\nbootstrap_complete: true\n"
	if err := os.WriteFile(filepath.Join(home, ".erun", "bootstrap.yaml"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	marker, found, err := LoadRemoteInitMarker(home, "team", "dev")
	if err != nil {
		t.Fatalf("LoadRemoteInitMarker failed: %v", err)
	}
	if !found {
		t.Fatalf("expected legacy marker to be returned when tenant/env match")
	}
	if marker.Tenant != "team" || marker.Environment != "dev" {
		t.Fatalf("unexpected legacy marker: %+v", marker)
	}
}

func TestLoadRemoteInitMarkerLegacyFallbackRejectsMismatch(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".erun"), 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacy := "tenant: other\nenvironment: dev\nproject_root: /repo\nbootstrap_complete: true\n"
	if err := os.WriteFile(filepath.Join(home, ".erun", "bootstrap.yaml"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	_, found, err := LoadRemoteInitMarker(home, "team", "dev")
	if err != nil {
		t.Fatalf("LoadRemoteInitMarker failed: %v", err)
	}
	if found {
		t.Fatalf("expected legacy marker to be rejected when its tenant/env differ from the requested ones")
	}
}

func TestSaveRemoteInitMarkerWritesPerTenantEnvPath(t *testing.T) {
	home := t.TempDir()
	marker := RemoteInitMarker{
		Tenant:            "team",
		Environment:       "dev",
		ProjectRoot:       "/repo",
		BootstrapComplete: true,
	}
	if err := SaveRemoteInitMarker(home, marker); err != nil {
		t.Fatalf("SaveRemoteInitMarker failed: %v", err)
	}
	want := filepath.Join(home, ".erun", "team", "dev", "bootstrap.yaml")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected marker at %s: %v", want, err)
	}
	legacy := filepath.Join(home, ".erun", "bootstrap.yaml")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("did not expect a marker to be written at the legacy path %s (err=%v)", legacy, err)
	}
}
