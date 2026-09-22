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

const gatewayURL = "https://openrouter.ai/api"

func TestHostClaudeGatewayCredentialReadsAuthToken(t *testing.T) {
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+gatewayURL+`","ANTHROPIC_AUTH_TOKEN":"  sk-or-v1-abc  ","ANTHROPIC_API_KEY":""}}`)
	got, ok := HostClaudeGatewayCredentialFor(gatewayURL)
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
	// A gateway may accept the key header rather than a bearer token. With no
	// auth token, the key is the credential in play — but only, as below, when
	// the host is pointed at the same gateway.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+gatewayURL+`","ANTHROPIC_API_KEY":"sk-or-v1-xyz"}}`)
	got, ok := HostClaudeGatewayCredentialFor(gatewayURL)
	if !ok || got != "sk-or-v1-xyz" {
		t.Fatalf("credential = (%q, %v), want the api key", got, ok)
	}
}

func TestHostClaudeGatewayCredentialSkipsBlankAPIKey(t *testing.T) {
	// A blank ANTHROPIC_API_KEY is a deliberate marker: Claude Code uses "" to
	// disable its direct-provider fallback. Reading it as a credential would
	// deliver an empty token and fail at the gateway instead.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+gatewayURL+`","ANTHROPIC_API_KEY":""}}`)
	if got, ok := HostClaudeGatewayCredentialFor(gatewayURL); ok {
		t.Fatalf("a blank api key must not read as a credential, got %q", got)
	}
}

// TestHostClaudeGatewayCredentialRequiresTheSameEndpoint pins the guard that
// keeps one service's credential out of another's hands. An operator
// authenticating directly against Anthropic has an ANTHROPIC_API_KEY in these
// settings and no base URL; delivering that to a gateway as its bearer token
// would disclose the key to a service it was never issued for.
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
	// A trailing slash and surrounding whitespace are not a different gateway,
	// so refusing them would decline the fallback for a key that is genuinely
	// this gateway's.
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
	// Naming the mismatch is the point of this read, so a host with no gateway
	// must say so with an empty string rather than a placeholder the caller
	// would render as an address.
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
			// Each of these is the ordinary unconfigured case, not a fault: this
			// supplies a default, so it reports "none" rather than failing.
			if got, ok := HostClaudeGatewayCredentialFor(gatewayURL); ok {
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
