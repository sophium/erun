package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildReportScenarios(t *testing.T) {
	t.Run("dry_run_with_platform_alias_traces_the_build_report", testBuildDryRunWithPlatformAliasTracesTheBuildReport)
	t.Run("real_run_with_platform_alias_reports_a_successful_build", testBuildRealRunWithPlatformAliasReportsASuccessfulBuild)
	t.Run("real_run_build_report_failure_does_not_fail_the_build", testBuildRealRunBuildReportFailureDoesNotFailTheBuild)
}

func testBuildDryRunWithPlatformAliasTracesTheBuildReport(t *testing.T) {
	// erun#1954: with an erun platform alias configured, build traces the
	// self-report it would make, never touching the network under
	// --dry-run -- so a fixed, unresolvable API URL (no real server
	// behind it) is fine here, the same choice review record-build's own
	// dry-run scenario makes, and keeps the golden free of a real
	// httptest server's randomly-assigned port. The commit resolved from
	// the seeded git repo is content-derived and differs per run;
	// normalize.Apply's existing <HEX> rule masks it the same way it
	// already masks other long hex digests.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	fixture.SeedGitRepo(t, setup.Cwd)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
	result := erun.Run(t, []string{"build", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_with_platform_alias_traces_the_build_report", normalize.Apply(result.Combined))
}

func testBuildRealRunWithPlatformAliasReportsASuccessfulBuild(t *testing.T) {
	// erun#1954: a real (non-dry-run) build with a platform alias
	// configured self-reports its outcome after it finishes. A real git
	// repository is seeded so the report resolves and includes a real
	// commit; the exact hash is masked by output normalization not
	// applying to it, so this asserts shape (commitId= present) rather
	// than the golden's exact bytes.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	fixture.SeedGitRepo(t, setup.Cwd)
	server := buildReportAPIStubServer(t, http.StatusCreated)
	platformAlias(t, setup, server)
	dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image) case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  buildx) case "$2" in inspect) echo "Platforms: linux/arm64*, linux/amd64" ;; *) exit 0 ;; esac ;;`,
		`  build) exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "--version", "1.0.0"}, erun.RunOptions{Cwd: dockerDir, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "platform: POST "+server.URL+"/v1/builds (environment=dev, successful=true, commitId=") {
		t.Fatalf("expected a traced build-report call with a resolved commitId, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "build reported to erun platform: build-1") {
		t.Fatalf("expected a successful build-report confirmation, got:\n%s", result.Combined)
	}
}

func testBuildRealRunBuildReportFailureDoesNotFailTheBuild(t *testing.T) {
	// erun#1954: a build must never fail -- or report itself as failed --
	// because self-reporting to the platform is unavailable.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	fixture.SeedGitRepo(t, setup.Cwd)
	server := buildReportAPIStubServer(t, http.StatusInternalServerError)
	platformAlias(t, setup, server)
	dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image) case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  buildx) case "$2" in inspect) echo "Platforms: linux/arm64*, linux/amd64" ;; *) exit 0 ;; esac ;;`,
		`  build) exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "--version", "1.0.0"}, erun.RunOptions{Cwd: dockerDir, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("a failed build report must not fail the build itself, got exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "build report to erun platform skipped:") {
		t.Fatalf("expected a recorded skip for the failed report, got:\n%s", result.Combined)
	}
}

// buildReportAPIStubServer runs a minimal erun-backend-api double covering
// GET /v1/environments and POST /v1/builds -- the two calls ReportBuildOutcome
// makes to self-report an ordinary `erun build` run (erun#1954). buildStatus
// lets a scenario force the report itself to fail (500) while still proving
// the build's own exit code is untouched.
func buildReportAPIStubServer(t testing.TB, buildStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"environmentId": "env-1", "tenantId": "tenant-1", "name": "dev", "type": "local-agent", "status": "running"},
		})
	})
	mux.HandleFunc("POST /v1/builds", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		if buildStatus != http.StatusCreated {
			http.Error(w, "platform unavailable", buildStatus)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"buildId": "build-1", "tenantId": "tenant-1", "environmentId": body["environmentId"],
			"successful": body["successful"], "commitId": body["commitId"], "version": body["version"],
			"createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
