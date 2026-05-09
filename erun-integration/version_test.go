package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func writeRuntimeRegistryConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write registry config: %v", err)
	}
}

func TestVersion(t *testing.T) {
	t.Run("no_registry", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version --no-registry exited %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "version/no_registry", normalize.Apply(result.Combined))
	})

	t.Run("time_flag_prints_elapsed", func(t *testing.T) {
		// Exercises feedback_render.go printElapsedTime: the --time flag must
		// emit an "elapsed:" line on stderr after a successful run. The
		// golden's normalize rule turns the variable duration into
		// `elapsed: <ELAPSED>`, locking both presence and format.
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry", "--time"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version --time exited %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "version/time_flag_prints_elapsed", normalize.Apply(result.Combined))
	})

	t.Run("version_file_in_cwd_overrides_build_info", func(t *testing.T) {
		// Exercises version.go resolveBuildInfo: when a VERSION file lives
		// in the current working directory, its contents must replace the
		// compiled-in version string in the output. The golden captures
		// `erun <VERSION>` (normalized from "9.9.9"); without VERSION the
		// compiled "dev" doesn't match the VERSION pattern and the diff
		// fails loudly.
		setup := env.New(t)
		if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
			t.Fatalf("write VERSION: %v", err)
		}
		result := erun.Run(t, []string{"version", "--no-registry"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "version/version_file_in_cwd_overrides_build_info", normalize.Apply(result.Combined))
	})

	t.Run("registry_dockerhub_stub_returns_latest_stable_and_snapshot", func(t *testing.T) {
		// Exercises eruncommon.ResolveDockerHubRuntimeRegistryVersions and
		// the tag-page pagination + tag classification helpers. A single
		// httptest.Server returns two paginated pages that include both
		// stable releases and a snapshot, and the version command must
		// surface the latest of each on stdout.
		page1Path := "/v2/repositories/acme/erun-devops/tags?page_size=100"
		var page2Path string
		var stableLatest, snapshotLatest string
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path + "?" + r.URL.RawQuery {
			case page1Path:
				next := server.URL + page2Path
				stableLatest = "1.4.0"
				snapshotLatest = "1.5.0-snapshot-20260101000000"
				fmt.Fprintf(w, `{"next": %q, "results":[{"name":"1.3.9"},{"name":"1.4.0"},{"name":"latest"}]}`, next)
			case strings.TrimPrefix(page2Path, ""):
				fmt.Fprintf(w, `{"next":"","results":[{"name":"1.5.0-snapshot-20260101000000"},{"name":"1.5.0-snapshot-20251231000000"}]}`)
			default:
				http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
			}
		}))
		t.Cleanup(server.Close)
		page2Path = "/v2/repositories/acme/erun-devops/tags?page=2"

		setup := env.New(t)
		writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
			"  namespace: acme\n"+
			"  repository: erun-devops\n"+
			"  baseurl: "+server.URL+"\n")
		result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// These are substring-asserted (not golden-locked) because the
		// surrounding normalize rules collapse 1.4.0 / 1.5.0-snapshot-...
		// to <VERSION>, which would erase the actual stub-driven values
		// the test cares about.
		if !strings.Contains(result.Stdout, "latest stable: "+stableLatest) {
			t.Errorf("expected stdout to include latest stable %q, got:\n%s", stableLatest, result.Stdout)
		}
		if !strings.Contains(result.Stdout, "latest snapshot: "+snapshotLatest) {
			t.Errorf("expected stdout to include latest snapshot %q, got:\n%s", snapshotLatest, result.Stdout)
		}
	})

	t.Run("registry_ghcr_stub_returns_latest_stable", func(t *testing.T) {
		// Exercises eruncommon.ResolveGHCRRuntimeRegistryVersions: a fake
		// GHCR token endpoint hands out a token; the v2 tags/list endpoint
		// returns paginated JSON; nextLinkFromHeader parses the rel="next"
		// link to drive a second page. Asserts the parsed latest stable
		// is surfaced on stdout.
		var server *httptest.Server
		var requested []string
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requested = append(requested, r.URL.Path+"?"+r.URL.RawQuery)
			switch {
			case r.URL.Path == "/token":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"token":"stub-token"}`)
			case r.URL.Path == "/v2/acme/erun-devops/tags/list" && r.URL.RawQuery == "":
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Link", `</v2/acme/erun-devops/tags/list?last=1.4.0>; rel="next"`)
				fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.3.9","1.4.0","latest"]}`)
			case r.URL.Path == "/v2/acme/erun-devops/tags/list" && r.URL.RawQuery == "last=1.4.0":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.4.1"]}`)
			default:
				http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
			}
		}))
		t.Cleanup(server.Close)

		setup := env.New(t)
		writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
			"  namespace: ghcr.io/acme\n"+
			"  repository: erun-devops\n"+
			"  baseurl: "+server.URL+"\n"+
			"  tokenurl: "+server.URL+"\n")
		result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stdout, "latest stable: 1.4.1") {
			t.Errorf("expected stdout to include latest stable 1.4.1 from paginated GHCR response, got:\n%s", result.Stdout)
		}
		// Sanity: confirm the runner actually exercised the token + both
		// pages so the test fails informatively if the pagination link
		// parser regresses.
		expected := map[string]bool{
			"/token?service=ghcr.io&scope=repository:acme/erun-devops:pull": false,
			"/v2/acme/erun-devops/tags/list?":                               false,
			"/v2/acme/erun-devops/tags/list?last=1.4.0":                     false,
		}
		for _, req := range requested {
			expected[req] = true
		}
		for path, hit := range expected {
			if !hit {
				t.Errorf("expected stub to receive %q, got requests:\n%v", path, requested)
			}
		}
	})

	t.Run("verbose_flag_prints_audit", func(t *testing.T) {
		// Exercises feedback_render.go auditCommand: with -v but without
		// --dry-run, the audit line must appear on stderr.
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry", "-v"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version -v exited %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "version/verbose_flag_prints_audit", normalize.Apply(result.Combined))
	})
}
