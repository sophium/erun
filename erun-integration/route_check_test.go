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

// routeCheckStubServer stands in for a deployed erun-backend-api plane:
// registered maps a path to the one method that answers 200 (bearer
// checked); every other path -- and every other method on a registered
// path -- falls through to Go's stdlib net/http.ServeMux own default
// behavior (405 for a path known under a different method, 404 for a path
// it has never heard of), the same behavior the real plane's bare mux
// produces since erun-backend-api installs no custom NotFoundHandler.
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

// seedRouteCheckRoutesDir writes a minimal internal/routes-shaped source
// file under dir so DiscoverAPIRoutes has real register(...) call sites to
// parse, without depending on this checkout's own erun-backend-api tree
// (the harness has no live cluster or cloud target, and pointing this
// command at a real monorepo layout from inside the sandboxed fixture would
// be exactly that kind of dependency). It includes one parameterized route
// so the {param} -> literal-segment substitution is exercised too.
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

// TestExecRouteCheck drives `erun exec route-check` against a stub plane
// standing in for a real deployed control plane: a route the plane answers
// under a different method (405) still counts reachable, a route the
// plane's router has never heard of (Go's own literal 404 page) is reported
// missing and fails the run, and a plane that does not even answer the
// GET /v1/whoami sanity probe refuses outright rather than reporting every
// route in the inventory as missing.
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
		// missing-on-plane is registered under POST here, never GET -- proving
		// that a 405 (path known, wrong method) still counts as reachable.
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
		// missing-on-plane is never registered on the stub at all, modeling a
		// route that merged and closed its issue while the deployed plane
		// still predates it.
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
		// No /v1/whoami registered at all: the plane answers Go's own default
		// 404 to the sanity probe itself, modeling a down or misconfigured
		// plane -- this must refuse rather than reporting every route in the
		// inventory as missing.
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
