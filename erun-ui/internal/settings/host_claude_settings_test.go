package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostGatewayDefaultsFromEnvReads(t *testing.T) {
	got := hostGatewayDefaultsFromEnv(map[string]string{
		"ANTHROPIC_BASE_URL":             "https://openrouter.ai/api",
		"ANTHROPIC_MODEL":                "deepseek/deepseek-v4.1-flash",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1048576",
		"ANTHROPIC_AUTH_TOKEN":           "sk-or-v1-must-not-be-carried",
		"ANTHROPIC_API_KEY":              "",
	})
	if got.BaseURL != "https://openrouter.ai/api" {
		t.Fatalf("base URL = %q", got.BaseURL)
	}
	if got.Model != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("model = %q", got.Model)
	}
	if got.Context != 1048576 {
		t.Fatalf("context = %d, want 1048576", got.Context)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), "sk-or-v1") {
		t.Fatalf("a credential value reached the defaults read model: %+v", got)
	}
}

func TestCredentialHintWithholdsShortValues(t *testing.T) {
	for _, value := range []string{"", "  ", "abc", "abcd"} {
		if got := credentialHint(value); got != "" {
			t.Fatalf("credentialHint(%q) = %q, want empty", value, got)
		}
	}
}

func TestHostGatewayDefaultsFromEnvTrims(t *testing.T) {
	got := hostGatewayDefaultsFromEnv(map[string]string{
		"ANTHROPIC_BASE_URL":             "  https://openrouter.ai/api  ",
		"ANTHROPIC_MODEL":                "  a/b  ",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS": " 100 ",
	})
	if got.BaseURL != "https://openrouter.ai/api" || got.Model != "a/b" || got.Context != 100 {
		t.Fatalf("got %+v", got)
	}
}

func TestHostGatewayDefaultsFromEnvNoBaseURLOffersNothing(t *testing.T) {
	for _, env := range []map[string]string{
		nil,
		{},
		{"ANTHROPIC_MODEL": "deepseek/deepseek-v4.1-flash"},
		{"ANTHROPIC_BASE_URL": "   "},
	} {
		if got := hostGatewayDefaultsFromEnv(env); got.BaseURL != "" {
			t.Fatalf("env %v offered %+v, want nothing", env, got)
		}
	}
}

func TestHostGatewayDefaultsFromEnvUnusableWindow(t *testing.T) {
	for _, raw := range []string{"", "  ", "not-a-number", "0", "-5"} {
		got := hostGatewayDefaultsFromEnv(map[string]string{
			"ANTHROPIC_BASE_URL":             "https://openrouter.ai/api",
			"ANTHROPIC_MODEL":                "a/b",
			"CLAUDE_CODE_MAX_CONTEXT_TOKENS": raw,
		})
		if got.Context != 0 {
			t.Fatalf("window %q yielded %d, want 0", raw, got.Context)
		}
	}
}

func withSettingsDir(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
			t.Fatalf("write settings: %v", err)
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

func TestLoadHostGatewayDefaultsReadsSettingsFile(t *testing.T) {
	withSettingsDir(t, `{"env":{"ANTHROPIC_BASE_URL":"https://openrouter.ai/api","ANTHROPIC_MODEL":"a/b","CLAUDE_CODE_MAX_CONTEXT_TOKENS":"4096"}}`)
	got, err := (&Service{}).LoadHostGatewayDefaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BaseURL != "https://openrouter.ai/api" || got.Model != "a/b" || got.Context != 4096 {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadHostGatewayDefaultsOffersNothingWithoutOne(t *testing.T) {
	for name, body := range map[string]string{
		"no settings file":      "",
		"malformed settings":    "{not json",
		"no env block":          `{"theme":"dark"}`,
		"env without a gateway": `{"env":{"ANTHROPIC_MODEL":"a/b"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			withSettingsDir(t, body)
			got, err := (&Service{}).LoadHostGatewayDefaults()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.BaseURL != "" {
				t.Fatalf("got %+v, want nothing", got)
			}
		})
	}
}
