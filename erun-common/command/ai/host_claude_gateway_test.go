package ai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// stubClaudeSettings isolates the settings read from the host machine.
func stubClaudeSettings(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(deploy.ClaudeConfigDirEnv, dir)
	if body == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

const gatewayURL = "https://openrouter.ai/api"

func TestHostClaudeGatewayCredentialReadsAuthToken(t *testing.T) {
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+gatewayURL+`","ANTHROPIC_AUTH_TOKEN":"  sk-or-v1-abc  ","ANTHROPIC_API_KEY":""}}`)
	got, ok := HostClaudeGatewayCredentialFor(gatewayURL)
	if !ok {
		t.Fatal("a settings file carrying an auth token must report one")
	}
	if got != "sk-or-v1-abc" {
		t.Fatalf("credential = %q, want the trimmed token", got)
	}
}

func TestHostClaudeGatewayCredentialFallsBackToAPIKey(t *testing.T) {
	// With no auth token, the API key is the credential.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+gatewayURL+`","ANTHROPIC_API_KEY":"sk-or-v1-xyz"}}`)
	got, ok := HostClaudeGatewayCredentialFor(gatewayURL)
	if !ok || got != "sk-or-v1-xyz" {
		t.Fatalf("credential = (%q, %v), want the api key", got, ok)
	}
}

func TestHostClaudeGatewayCredentialSkipsBlankAPIKey(t *testing.T) {
	// Claude Code uses a blank ANTHROPIC_API_KEY to disable its direct fallback.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+gatewayURL+`","ANTHROPIC_API_KEY":""}}`)
	if got, ok := HostClaudeGatewayCredentialFor(gatewayURL); ok {
		t.Fatalf("a blank api key must not read as a credential, got %q", got)
	}
}

func TestHostClaudeGatewayCredentialRequiresTheSameEndpoint(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"a direct Anthropic key, no gateway configured", `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-x"}}`},
		{"a token for a different gateway", `{"env":{"ANTHROPIC_BASE_URL":"https://other.example.com","ANTHROPIC_AUTH_TOKEN":"t"}}`},
		{"the same host under a different path", `{"env":{"ANTHROPIC_BASE_URL":"` + gatewayURL + `/v2","ANTHROPIC_AUTH_TOKEN":"t"}}`},
		{"the same address with a different scheme", `{"env":{"ANTHROPIC_BASE_URL":"http://openrouter.ai/api","ANTHROPIC_AUTH_TOKEN":"t"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubClaudeSettings(t, tc.body)
			if got, ok := HostClaudeGatewayCredentialFor(gatewayURL); ok {
				t.Fatalf("a key for another endpoint must not be reused, got %q", got)
			}
		})
	}
}

func TestHostClaudeGatewayCredentialToleratesSpellingOfTheSameEndpoint(t *testing.T) {
	for _, spelling := range []string{
		gatewayURL,
		gatewayURL + "/",
		"  " + gatewayURL + "  ",
		"https://OpenRouter.ai/api",
	} {
		stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+spelling+`","ANTHROPIC_AUTH_TOKEN":"sk-or-v1-abc"}}`)
		got, ok := HostClaudeGatewayCredentialFor(gatewayURL)
		if !ok || got != "sk-or-v1-abc" {
			t.Fatalf("spelling %q = (%q, %v), want the token", spelling, got, ok)
		}
	}
}

func TestHostClaudeGatewayEndpoint(t *testing.T) {
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"  `+gatewayURL+`  "}}`)
	if got := HostClaudeGatewayEndpoint(); got != gatewayURL {
		t.Fatalf("endpoint = %q, want the trimmed URL", got)
	}
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_API_KEY":"sk-ant-x"}}`)
	if got := HostClaudeGatewayEndpoint(); got != "" {
		t.Fatalf("endpoint = %q, want empty for a host on no gateway", got)
	}
}

func TestHostClaudeGatewayCredentialWithoutSettings(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no settings file", ""},
		{"settings without an env block", `{"model":"opus"}`},
		{"settings with neither credential", `{"env":{"ANTHROPIC_BASE_URL":"` + gatewayURL + `"}}`},
		{"malformed settings", `{"env":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubClaudeSettings(t, tc.body)
			// Unconfigured cases report "none", not an error.
			if got, ok := HostClaudeGatewayCredentialFor(gatewayURL); ok {
				t.Fatalf("credential = %q, want none", got)
			}
		})
	}
}

func TestHostClaudeSettingsEnvFindsTheCredentialInTheSameRead(t *testing.T) {
	// Gateway and key must come from one read of the settings file.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"https://openrouter.ai/api","ANTHROPIC_AUTH_TOKEN":"sk-or-v1-abc"}}`)
	env, err := deploy.HostClaudeSettingsEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://openrouter.ai/api" {
		t.Fatalf("base URL = %q", env["ANTHROPIC_BASE_URL"])
	}
	if got, ok := deploy.GatewayCredentialFromEnv(env); !ok || got != "sk-or-v1-abc" {
		t.Fatalf("credential = (%q, %v), want the token from the same read", got, ok)
	}
}
