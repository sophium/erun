package config

import (
	"strings"
	"testing"
)

func TestOpenRouterConfigGatewayEnvVars(t *testing.T) {
	c := &OpenRouterConfig{BaseURL: "  https://openrouter.ai/api  ", AuthTokenRef: "claude-gateway"}
	env := c.GatewayEnvVars()
	if env["ANTHROPIC_BASE_URL"] != "https://openrouter.ai/api" {
		t.Fatalf("base URL = %q, want the trimmed value", env["ANTHROPIC_BASE_URL"])
	}
	// Present-but-empty: unset lets Claude Code bypass the gateway.
	if value, ok := env["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY must be present and empty, got %q (present=%v)", value, ok)
	}
	// The credential and its ref never belong in the settings env block.
	for key, value := range env {
		if strings.Contains(key, "TOKEN") || strings.Contains(value, "claude-gateway") {
			t.Fatalf("a credential or its reference leaked into the env block: %s=%q", key, value)
		}
	}
}
