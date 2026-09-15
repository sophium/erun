package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

// stubClaudeSettings points the Claude Code settings read at a directory this
// test owns, so the machine running the suite cannot decide the outcome.
func stubClaudeSettings(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(claudeConfigDirEnv, dir)
	if body == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

func TestHostClaudeGatewayCredentialReadsAuthToken(t *testing.T) {
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"  sk-or-v1-abc  ","ANTHROPIC_API_KEY":""}}`)
	got, ok := HostClaudeGatewayCredential()
	if !ok {
		t.Fatal("a settings file carrying an auth token must report one")
	}
	// Trimmed, because a trailing newline pasted into settings is common and
	// would otherwise be delivered as part of the credential.
	if got != "sk-or-v1-abc" {
		t.Fatalf("credential = %q, want the trimmed token", got)
	}
}

func TestHostClaudeGatewayCredentialFallsBackToAPIKey(t *testing.T) {
	// Claude Code accepts either header for a gateway. With no auth token, the
	// key is the credential in play.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_API_KEY":"sk-ant-xyz"}}`)
	got, ok := HostClaudeGatewayCredential()
	if !ok || got != "sk-ant-xyz" {
		t.Fatalf("credential = (%q, %v), want the api key", got, ok)
	}
}

func TestHostClaudeGatewayCredentialSkipsBlankAPIKey(t *testing.T) {
	// A blank ANTHROPIC_API_KEY is a deliberate marker: Claude Code uses "" to
	// disable its direct-provider fallback. Reading it as a credential would
	// deliver an empty token and fail at the gateway instead.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_API_KEY":""}}`)
	if got, ok := HostClaudeGatewayCredential(); ok {
		t.Fatalf("a blank api key must not read as a credential, got %q", got)
	}
}

func TestHostClaudeGatewayCredentialWithoutSettings(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no settings file", ""},
		{"settings without an env block", `{"model":"opus"}`},
		{"settings with neither credential", `{"env":{"ANTHROPIC_BASE_URL":"https://x"}}`},
		{"malformed settings", `{"env":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubClaudeSettings(t, tc.body)
			// Each of these is the ordinary unconfigured case, not a fault: this
			// supplies a default, so it reports "none" rather than failing.
			if got, ok := HostClaudeGatewayCredential(); ok {
				t.Fatalf("credential = %q, want none", got)
			}
		})
	}
}

func TestHostClaudeSettingsEnvFindsTheCredentialInTheSameRead(t *testing.T) {
	// Everything a catalog derives comes from this one read, so the gateway and
	// the key it will be used with cannot come from two different snapshots of
	// the settings file.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"https://openrouter.ai/api","ANTHROPIC_AUTH_TOKEN":"sk-or-v1-abc"}}`)
	env, err := HostClaudeSettingsEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://openrouter.ai/api" {
		t.Fatalf("base URL = %q", env["ANTHROPIC_BASE_URL"])
	}
	if got, ok := GatewayCredentialFromEnv(env); !ok || got != "sk-or-v1-abc" {
		t.Fatalf("credential = (%q, %v), want the token from the same read", got, ok)
	}
}
