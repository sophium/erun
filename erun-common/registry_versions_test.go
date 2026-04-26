package eruncommon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatestRuntimeVersionsFromTags(t *testing.T) {
	got := latestRuntimeVersionsFromTags([]string{
		"latest",
		"1.0.49",
		"1.0.51-snapshot-20260424080000",
		"1.0.50",
		"1.0.51-snapshot-20260424100000",
		"1.0.9",
	})

	if got.LatestStable != "1.0.50" {
		t.Fatalf("unexpected latest stable: %+v", got)
	}
	if got.LatestSnapshot != "1.0.51-snapshot-20260424100000" {
		t.Fatalf("unexpected latest snapshot: %+v", got)
	}
	if !got.HasVersion("1.0.50") || !got.HasVersion("1.0.51-snapshot-20260424100000") {
		t.Fatalf("expected tag set to include discovered tags, got %+v", got)
	}
}

func TestRuntimeVersionSuggestionsUseRegistryVersions(t *testing.T) {
	got := RuntimeVersionSuggestions(BuildInfo{Version: "1.0.50"}, RuntimeRegistryVersions{
		LatestStable:   "1.0.50",
		LatestSnapshot: "1.0.51-snapshot-20260424100000",
	})
	want := []RuntimeVersionSuggestion{
		{Label: "Current", Version: "1.0.50"},
		{Label: "Previous", Version: "1.0.49"},
		{Label: "Last snapshot", Version: "1.0.51-snapshot-20260424100000"},
	}
	if strings.Join(formatSuggestions(got), "\n") != strings.Join(formatSuggestions(want), "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", got, want)
	}
}

func TestRuntimeVersionSuggestionsIncludeLatestStableWhenDifferentFromCurrent(t *testing.T) {
	got := RuntimeVersionSuggestions(BuildInfo{Version: "dev"}, RuntimeRegistryVersions{
		LatestStable:   "1.0.50",
		LatestSnapshot: "1.0.51-snapshot-20260424100000",
	})
	want := []RuntimeVersionSuggestion{
		{Label: "Current", Version: "dev"},
		{Label: "Latest stable", Version: "1.0.50"},
		{Label: "Previous", Version: "1.0.49"},
		{Label: "Last snapshot", Version: "1.0.51-snapshot-20260424100000"},
	}
	if strings.Join(formatSuggestions(got), "\n") != strings.Join(formatSuggestions(want), "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", got, want)
	}
}

func TestRuntimeDeployVersionSuggestionsOnlyIncludeTagsPresentInImageRepository(t *testing.T) {
	got := RuntimeDeployVersionSuggestions(BuildInfo{Version: "1.0.50"}, RuntimeRegistryVersions{
		Tags:           []string{"1.0.49", "1.0.50-snapshot-20260425114506"},
		LatestStable:   "1.0.49",
		LatestSnapshot: "1.0.50-snapshot-20260425114506",
	})
	want := []RuntimeVersionSuggestion{
		{Label: "Latest stable", Version: "1.0.49"},
		{Label: "Last snapshot", Version: "1.0.50-snapshot-20260425114506"},
	}
	if strings.Join(formatSuggestions(got), "\n") != strings.Join(formatSuggestions(want), "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", got, want)
	}
}

func TestResolveDockerHubRuntimeRegistryVersionsFollowsPages(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "2":
			_, _ = w.Write([]byte(`{"next":"","results":[{"name":"1.0.50"},{"name":"1.0.51-snapshot-20260424100000"}]}`))
		default:
			next := server.URL + "/v2/repositories/erunpaas/erun-devops/tags?page=2"
			_, _ = w.Write([]byte(`{"next":` + quoteJSON(next) + `,"results":[{"name":"1.0.49"},{"name":"1.0.51-snapshot-20260424080000"}]}`))
		}
	}))
	defer server.Close()

	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})
	client := &http.Client{Transport: transport}

	got, err := ResolveDockerHubRuntimeRegistryVersions(context.Background(), client, "erunpaas", "erun-devops")
	if err != nil {
		t.Fatalf("ResolveDockerHubRuntimeRegistryVersions failed: %v", err)
	}
	if got.Image != "erunpaas/erun-devops" || got.LatestStable != "1.0.50" || got.LatestSnapshot != "1.0.51-snapshot-20260424100000" || !got.HasVersion("1.0.49") {
		t.Fatalf("unexpected versions: %+v", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func quoteJSON(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func formatSuggestions(values []RuntimeVersionSuggestion) []string {
	formatted := make([]string, 0, len(values))
	for _, value := range values {
		formatted = append(formatted, value.Label+"="+value.Version)
	}
	return formatted
}
