package ai

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/deploy/gateway"
)

const testGatewayURL = "https://openrouter.ai/api"

type failingSecretStore struct{ err error }

func (s failingSecretStore) SaveCloudSecret(string, string) error { return nil }
func (s failingSecretStore) LoadCloudSecret(string) (string, error) {
	return "", s.err
}
func (s failingSecretStore) DeleteCloudSecret(string) error { return nil }

func emptySecretStore(t *testing.T) seam.CloudSecretStore {
	t.Helper()
	return cloud.NewFileCloudSecretStore(t.TempDir())
}

func TestResolveGatewayCredentialPrefersTheSavedValue(t *testing.T) {
	// A saved key wins over the host's key for the same gateway.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+testGatewayURL+`","ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	store := cloud.NewFileCloudSecretStore(t.TempDir())
	if err := store.SaveCloudSecret("claude-gateway", "saved-by-operator"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := gateway.ResolveGatewayCredentialFromStore(store, "claude-gateway", testGatewayURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "saved-by-operator" {
		t.Fatalf("credential = %q, want the saved value", got)
	}
}

func TestResolveGatewayCredentialReusesTheHostKeyForTheSameGateway(t *testing.T) {
	// Nothing saved: fall back to the host key for the same gateway.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+testGatewayURL+`","ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	got, err := gateway.ResolveGatewayCredentialFromStore(emptySecretStore(t), "claude-gateway", testGatewayURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-host" {
		t.Fatalf("credential = %q, want the host key", got)
	}
}

// A host key is scoped to its endpoint; sending it to another gateway would disclose it.
func TestResolveGatewayCredentialNeverSendsAHostKeyToAnotherGateway(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{
			name: "a direct Anthropic key with no gateway configured",
			env:  `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-secret"}}`,
		},
		{
			name: "a bearer token for a different gateway",
			env:  `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.com/anthropic","ANTHROPIC_AUTH_TOKEN":"other-gateway-token"}}`,
		},
		{
			name: "a key whose endpoint differs only by path",
			env:  `{"env":{"ANTHROPIC_BASE_URL":"` + testGatewayURL + `/v2","ANTHROPIC_AUTH_TOKEN":"wrong-path-token"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubClaudeSettings(t, tc.env)
			got, err := gateway.ResolveGatewayCredentialFromStore(emptySecretStore(t), "claude-gateway", testGatewayURL)
			if err == nil {
				t.Fatalf("a key for another endpoint must not be delivered, got %q", got)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") {
				for _, leak := range []string{"sk-ant-api03-secret", "other-gateway-token", "wrong-path-token"} {
					if strings.Contains(err.Error(), leak) {
						t.Fatalf("the credential leaked into the error: %v", err)
					}
				}
			}
			// The refusal must not read as "no key exists".
			if !strings.Contains(err.Error(), "Claude Code") {
				t.Fatalf("the refusal does not mention the host's own key: %v", err)
			}
			if strings.Contains(err.Error(), "settings carry none") {
				t.Fatalf("a host holding a key must not be told it holds none: %v", err)
			}
		})
	}
}

func TestResolveGatewayCredentialExplainsADirectAnthropicHost(t *testing.T) {
	// A host key with no gateway: the refusal must still mention it.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-secret"}}`)
	_, err := gateway.ResolveGatewayCredentialFromStore(emptySecretStore(t), "claude-gateway", testGatewayURL)
	if err == nil {
		t.Fatal("a direct-Anthropic key must not be delivered to a gateway")
	}
	if !strings.Contains(err.Error(), "authenticates directly") {
		t.Fatalf("the refusal does not explain the host's own setup: %v", err)
	}
}

func TestResolveGatewayCredentialNamesBothSourcesWhenNeitherExists(t *testing.T) {
	stubClaudeSettings(t, "")
	_, err := gateway.ResolveGatewayCredentialFromStore(emptySecretStore(t), "claude-gateway", testGatewayURL)
	if err == nil {
		t.Fatal("no credential anywhere must be an error, not an empty Secret")
	}
	for _, want := range []string{"claude-gateway", "Claude Code"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestResolveGatewayCredentialReportsAnUnreadableStore(t *testing.T) {
	// An unreadable store might hold the winning value, so resolution stops.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+testGatewayURL+`","ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	want := errors.New("permission denied")
	_, err := gateway.ResolveGatewayCredentialFromStore(failingSecretStore{err: want}, "claude-gateway", testGatewayURL)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want it to wrap %v", err, want)
	}
	if strings.Contains(err.Error(), "from-host") {
		t.Fatalf("the host key was delivered despite the fault: %v", err)
	}
}
