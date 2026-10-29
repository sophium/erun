package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// routeCheckStubServer answers 200 for each registered path and method; everything else
// gets ServeMux's default 405/404, as the real plane has no custom NotFoundHandler.
func routeCheckStubServer(t testing.TB, registered map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, method := range registered {
		mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
			if !requireBearer(w, r) {
				return
			}
			w.WriteHeader(http.StatusOK)
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// seedRouteCheckRoutesDir writes a minimal routes source, with one {param} route, so
// discovery does not depend on this checkout's backend tree.
func seedRouteCheckRoutesDir(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	const source = `package routes

import "net/http"

func RegisterExampleRoutes(register ProtectedRouteRegistrar) {
	register(http.MethodGet, "/v1/whoami", http.HandlerFunc(nil))
	register(http.MethodGet, "/v1/known", http.HandlerFunc(nil))
	register(http.MethodPost, "/v1/missing-on-plane", http.HandlerFunc(nil))
	register(http.MethodGet, "/v1/reviews/{review_id}", http.HandlerFunc(nil))
}
`
	if err := os.WriteFile(filepath.Join(dir, "example_routes.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write routes source: %v", err)
	}
}

// TestExecRouteCheck: a 405 counts as reachable, Go's 404 page as missing, and a plane
// failing the GET /v1/whoami probe refuses outright.
func TestExecRouteCheck(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		result := erun.Run(t, []string{"exec", "route-check", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/route_check_help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_inventory_without_probing", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		routesDir := filepath.Join(setup.Cwd, "stub-routes")
		seedRouteCheckRoutesDir(t, routesDir)
		platform := routeCheckStubServer(t, map[string]string{"/v1/whoami": "GET"})
		platformAlias(t, setup, platform)
		result := erun.Run(t, []string{
			"exec", "route-check", "--routes-dir", routesDir, "--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/route_check_dry_run_traces_inventory_without_probing",
			normalize.Apply(result.Combined, stubServerRule(platform, "<PLATFORM_API>")))
	})

	t.Run("real_run_all_routes_reachable_exits_zero", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		routesDir := filepath.Join(setup.Cwd, "stub-routes")
		seedRouteCheckRoutesDir(t, routesDir)
		// Registered under POST only: a 405 still counts as reachable.
		platform := routeCheckStubServer(t, map[string]string{
			"/v1/whoami":                    "GET",
			"/v1/known":                     "GET",
			"/v1/missing-on-plane":          "POST",
			"/v1/reviews/route-check-probe": "GET",
		})
		platformAlias(t, setup, platform)
		result := erun.Run(t, []string{
			"exec", "route-check", "--routes-dir", routesDir,
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "exec/route_check_real_run_all_routes_reachable_exits_zero",
			normalize.Apply(result.Combined, stubServerRule(platform, "<PLATFORM_API>")))
	})

	t.Run("real_run_reports_a_missing_route_and_exits_non_zero", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		routesDir := filepath.Join(setup.Cwd, "stub-routes")
		seedRouteCheckRoutesDir(t, routesDir)
		// Never registered: the deployed plane predates the route.
		platform := routeCheckStubServer(t, map[string]string{
			"/v1/whoami":                    "GET",
			"/v1/known":                     "GET",
			"/v1/reviews/route-check-probe": "GET",
		})
		platformAlias(t, setup, platform)
		result := erun.Run(t, []string{
			"exec", "route-check", "--routes-dir", routesDir,
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing route, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/route_check_real_run_reports_a_missing_route_and_exits_non_zero",
			normalize.Apply(result.Combined, stubServerRule(platform, "<PLATFORM_API>")))
	})

	t.Run("real_run_refuses_when_the_plane_does_not_answer_the_sanity_probe", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		routesDir := filepath.Join(setup.Cwd, "stub-routes")
		seedRouteCheckRoutesDir(t, routesDir)
		// No /v1/whoami: a down plane must refuse rather than report every route missing.
		platform := routeCheckStubServer(t, map[string]string{})
		platformAlias(t, setup, platform)
		result := erun.Run(t, []string{
			"exec", "route-check", "--routes-dir", routesDir,
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unreachable plane, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "exec/route_check_real_run_refuses_when_the_plane_does_not_answer_the_sanity_probe",
			normalize.Apply(result.Combined, stubServerRule(platform, "<PLATFORM_API>")))
	})
}
