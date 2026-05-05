package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureClaudeSettingsCreatesFile(t *testing.T) {
	dir := t.TempDir()
	if err := ensureClaudeSettingsWithHomeDir(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("expected settings.json to be created: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	assertBypassPermissions(t, settings)
}

func TestEnsureClaudeSettingsPreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	existing := `{"theme":"dark"}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := ensureClaudeSettingsWithHomeDir(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := settings["theme"]; !ok {
		t.Fatalf("expected 'theme' field to be preserved, got: %s", data)
	}
	assertBypassPermissions(t, settings)
}

func TestEnsureClaudeSettingsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := ensureClaudeSettingsWithHomeDir(dir); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	assertBypassPermissions(t, settings)
}

func TestEnsureClaudeSettingsUpdatesPartialSettings(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	partial := `{"permissions":{"defaultMode":"default"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(partial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := ensureClaudeSettingsWithHomeDir(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	assertBypassPermissions(t, settings)
}

func assertBypassPermissions(t *testing.T, settings map[string]json.RawMessage) {
	t.Helper()
	permData, ok := settings["permissions"]
	if !ok {
		t.Fatal("expected 'permissions' field")
	}
	var perms map[string]json.RawMessage
	if err := json.Unmarshal(permData, &perms); err != nil {
		t.Fatalf("invalid permissions JSON: %v", err)
	}
	var mode string
	if err := json.Unmarshal(perms["defaultMode"], &mode); err != nil || mode != "bypassPermissions" {
		t.Fatalf("expected permissions.defaultMode to be 'bypassPermissions', got: %s", perms["defaultMode"])
	}
	var skip bool
	if err := json.Unmarshal(settings["skipDangerousModePermissionPrompt"], &skip); err != nil || !skip {
		t.Fatalf("expected skipDangerousModePermissionPrompt to be true, got: %s", settings["skipDangerousModePermissionPrompt"])
	}
}
