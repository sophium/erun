package main

import (
	"testing"
)

func TestGatewayModelsURL(t *testing.T) {
	t.Run("appends the discovery path and its bound to the base URL", func(t *testing.T) {
		// The same request Claude Code's own discovery makes, so a gateway that
		// answers one answers the other.
		got, err := gatewayModelsURL("https://openrouter.ai/api")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://openrouter.ai/api/v1/models?limit=1000" {
			t.Fatalf("url = %q", got)
		}
	})

	t.Run("keeps a base URL that already carries a path", func(t *testing.T) {
		// A gateway mounted under a prefix is addressed where it actually serves.
		got, err := gatewayModelsURL("https://gw.example.com/anthropic/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://gw.example.com/anthropic/v1/models?limit=1000" {
			t.Fatalf("url = %q", got)
		}
	})

	t.Run("refuses a base URL it cannot address", func(t *testing.T) {
		for _, base := range []string{"", "   ", "openrouter.ai/api", "/just/a/path"} {
			if _, err := gatewayModelsURL(base); err == nil {
				t.Fatalf("expected %q to be refused", base)
			}
		}
	})
}

func TestGatewayContextWindow(t *testing.T) {
	// The provider-level figure is the one the serving provider will accept, and
	// it can be smaller than the advertised maximum. Declaring the larger one
	// lets a conversation grow past what the provider takes, so the request
	// fails with a too-long error instead of compacting cleanly.
	cases := []struct {
		name              string
		provider, adverti int
		want              int
	}{
		{"provider figure wins when both are present", 1024000, 1048576, 1024000},
		{"advertised figure is used when the provider gives none", 0, 1048576, 1048576},
		{"neither present is unknown, not zero-by-assumption", 0, 0, 0},
		{"a provider figure alone is enough", 200000, 0, 200000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gatewayContextWindow(tc.provider, tc.adverti); got != tc.want {
				t.Fatalf("gatewayContextWindow(%d, %d) = %d, want %d", tc.provider, tc.adverti, got, tc.want)
			}
		})
	}
}

// The first entry is OpenRouter's shape, which nests the provider-level window;
// the second carries only a top-level one.
const gatewayModelsFixture = `{"data":[
	{"id":"deepseek/deepseek-v4-pro-0813","display_name":"DeepSeek V4 Pro",
	 "description":"long context","context_length":1048576,
	 "top_provider":{"context_length":1024000}},
	{"id":"openai/gpt-6-astra","context_length":1050000}
]}`

func TestParseGatewayModelsReadsEntries(t *testing.T) {
	got, err := parseGatewayModels([]byte(gatewayModelsFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2", len(got))
	}
	if got[0].ID != "deepseek/deepseek-v4-pro-0813" || got[0].DisplayName != "DeepSeek V4 Pro" {
		t.Fatalf("first entry = %+v", got[0])
	}
	// The provider-level figure wins: it is what the serving provider accepts.
	if got[0].Context != 1024000 {
		t.Fatalf("provider-level window not preferred: %+v", got[0])
	}
	if got[1].Context != 1050000 {
		t.Fatalf("top-level window not read: %+v", got[1])
	}
}

func TestParseGatewayModelsMissingWindowIsUnknown(t *testing.T) {
	got, err := parseGatewayModels([]byte(`{"data":[{"id":"a/b"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Context != 0 {
		t.Fatalf("expected one entry with no window, got %+v", got)
	}
}

func TestParseGatewayModelsDropsBlanksAndDuplicates(t *testing.T) {
	got, err := parseGatewayModels([]byte(`{"data":[{"id":"a/b"},{"id":"  "},{"id":"a/b"},{"id":"c/d"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].ID != "a/b" || got[1].ID != "c/d" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseGatewayModelsEmptyDataListsNothing(t *testing.T) {
	// A gateway that serves the endpoint but lists nothing is not an error.
	for _, body := range []string{`{"data":[]}`, `{}`} {
		got, err := parseGatewayModels([]byte(body))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", body, err)
		}
		if len(got) != 0 {
			t.Fatalf("%s: got %+v, want none", body, got)
		}
	}
}

func TestParseGatewayModelsRejectsNonPayload(t *testing.T) {
	// A gateway that answers with an error page or a different schema must be
	// reported rather than silently listed as no models.
	for _, body := range []string{`<html>not json</html>`, `{"data":"not an array"}`} {
		if _, err := parseGatewayModels([]byte(body)); err == nil {
			t.Fatalf("expected %s to be reported", body)
		}
	}
}
