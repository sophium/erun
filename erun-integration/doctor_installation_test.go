package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorInstallationScenarios(t *testing.T) {
	t.Run("dry_run_reports_runtime_image_registry_mismatch", testDoctorDryRunReportsRuntimeImageRegistryMismatch)
	t.Run("dry_run_reports_runtime_image_line_mismatch", testDoctorDryRunReportsRuntimeImageLineMismatch)
	t.Run("dry_run_consistent_runtime_image_line_reports_nothing", testDoctorDryRunConsistentRuntimeImageLineReportsNothing)
	t.Run("dry_run_reports_gitignored_project_config", testDoctorDryRunReportsGitignoredProjectConfig)
	t.Run("dry_run_tracked_project_config_reports_nothing", testDoctorDryRunTrackedProjectConfigReportsNothing)
	t.Run("dry_run_reports_stale_desktop_app_bundle", testDoctorDryRunReportsStaleDesktopAppBundle)
	t.Run("dry_run_reports_the_desktop_app_erun_app_launches", testDoctorDryRunReportsTheDesktopAppErunAppLaunches)
	t.Run("dry_run_reports_shadowed_desktop_app_bundle", testDoctorDryRunReportsShadowedDesktopAppBundle)
	t.Run("dry_run_no_desktop_app_bundle_reports_nothing", testDoctorDryRunNoDesktopAppBundleReportsNothing)
}

func testDoctorDryRunReportsRuntimeImageRegistryMismatch(t *testing.T) {
	// #1328: a runtimeimage pinned to one registry (an ECR build host)
	// while runtimeregistry names another (the deploy/chart registry)
	// leaves the pod unable to pull unless a credential happens to
	// resolve for the image's own registry at deploy time. This is a
	// pure config comparison — no exec, no live read — so doctor reports
	// it identically in --dry-run and for real, naming both registries
	// and the fix.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	appendEnvConfig(t, setup, "team", "dev", "runtimeimage: 123456789012.dkr.ecr.eu-west-2.amazonaws.com/team-devops\nruntimeregistry: ghcr.io/sophium\n")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_runtime_image_registry_mismatch", normalize.Apply(result.Combined))
}

func testDoctorDryRunReportsRuntimeImageLineMismatch(t *testing.T) {
	// erun#1754: a runtimeimage naming the stock erun-devops image while
	// the env's last confirmed deploy (runtimerunningimage) actually ran a
	// tenant's own <tenant>-devops image is static config drift a health
	// check exists to catch -- comparing the two image fields needs no
	// live cluster read (a bare version number alone cannot name a
	// release line, but an image name always can), so doctor reports it
	// identically in --dry-run and for real.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	appendEnvConfig(t, setup, "team", "dev", "runtimeimage: ghcr.io/sophium/erun-devops\nruntimerunningimage: ghcr.io/sophium/team-devops:1.0.86\n")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_runtime_image_line_mismatch", normalize.Apply(result.Combined))
}

func testDoctorDryRunConsistentRuntimeImageLineReportsNothing(t *testing.T) {
	// The other half: a consistent pairing (both fields name the same
	// release line) must never be flagged, so an env riding the stock
	// image on purpose (frs/local's shape in the issue) sees no new
	// friction from this check.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	appendEnvConfig(t, setup, "team", "dev", "runtimeimage: erun-devops\nruntimerunningimage: ghcr.io/sophium/erun-devops:1.0.203\n")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "Runtime image release line") {
		t.Fatalf("expected no release-line finding for a consistent pairing, got:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_consistent_runtime_image_line_reports_nothing", normalize.Apply(result.Combined))
}

func testDoctorDryRunReportsGitignoredProjectConfig(t *testing.T) {
	// A project config that resolves from disk but is excluded by .gitignore
	// silently works on this machine only, since git never sees it. doctor
	// must report that under "== Project config ==" rather than staying silent.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedGitignoredProjectConfig(t, setup, "{}\n")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_gitignored_project_config", normalize.Apply(result.Combined))
}

func testDoctorDryRunTrackedProjectConfigReportsNothing(t *testing.T) {
	// The other half: a config that is present but not gitignored (the
	// common case) must never be flagged.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "{}\n")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "Project config") {
		t.Fatalf("expected no project-config finding for a tracked config, got:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_tracked_project_config_reports_nothing", normalize.Apply(result.Combined))
}

func testDoctorDryRunReportsStaleDesktopAppBundle(t *testing.T) {
	// The installed desktop app bundle can drift arbitrarily far
	// behind the CLI with nothing to say so. A single ~/Applications/ERun.app
	// bundle whose Info.plist version differs from this CLI's own build
	// version must surface under "== Desktop app ==", identically in
	// --dry-run and for real since this is a pure file read.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	writeDesktopAppBundle(t, filepath.Join(setup.Home, "Applications", "ERun.app"), "1.0.51")
	envVars := append(setup.Env(),
		"ERUN_HOST_OS_OVERRIDE=darwin",
		"ERUN_DESKTOP_APP_SYSTEM_APPLICATIONS_DIR_OVERRIDE="+filepath.Join(setup.Home, "no-system-applications"),
	)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "Multiple ERun.app bundles") {
		t.Fatalf("expected no multiple-bundle warning for a single bundle, got:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_stale_desktop_app_bundle", normalize.Apply(result.Combined))
}

func testDoctorDryRunReportsTheDesktopAppErunAppLaunches(t *testing.T) {
	// The dev wrapper writes the CLI and the desktop app into the same
	// BIN_DIR -- erun-cli/bin, or $ERUN_DEV_BIN_DIR -- and that is where a
	// running desktop actually came from, because `erun app`'s own sibling
	// lookup resolves it there. Doctor inspected only ~/Applications and
	// /Applications, so it reported on a bundle nothing was running and
	// stayed silent about the live one. Seeding a drifted bundle where the
	// CLI itself sits must surface it, and must say that this is the copy a
	// launch reaches. The executable-dir seam stands in for the shared
	// instrumented binary's own directory, which a scenario cannot write
	// beside without leaking a bundle into every other scenario in the run.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	binDir := filepath.Join(setup.Home, "erun-cli", "bin")
	writeDesktopAppBundle(t, filepath.Join(binDir, "ERun.app"), "1.0.51")
	envVars := append(setup.Env(),
		"ERUN_HOST_OS_OVERRIDE=darwin",
		"ERUN_DESKTOP_APP_SYSTEM_APPLICATIONS_DIR_OVERRIDE="+filepath.Join(setup.Home, "no-system-applications"),
		"ERUN_DESKTOP_APP_EXECUTABLE_DIR_OVERRIDE="+binDir,
	)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "erun app launches") {
		t.Fatalf("doctor inspected a copy beside the CLI but never said whether a launch reaches it:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "Multiple ERun.app bundles") {
		t.Fatalf("expected no multiple-bundle warning for a single bundle, got:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_the_desktop_app_erun_app_launches", normalize.Apply(result.Combined))
}

func testDoctorDryRunReportsShadowedDesktopAppBundle(t *testing.T) {
	// The operator-reported shape: a current bundle at
	// ~/Applications/ERun.app sits alongside a stale one at
	// /Applications/ERun.app (both simulated here since a test must never
	// touch a real /Applications -- ERUN_DESKTOP_APP_SYSTEM_APPLICATIONS_DIR_OVERRIDE
	// is the seam for that). Both share the bundle id com.sophium.erun, so
	// Finder/Spotlight/the Dock can launch either one regardless of which
	// is current -- doctor must name both bundles, flag the stale one
	// against this CLI's own version, and warn about the shadow-copy
	// hazard even though this CLI's own build (a "dev" build here) happens
	// to match one of the two.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	systemApplications := filepath.Join(setup.Home, "system-applications")
	writeDesktopAppBundle(t, filepath.Join(setup.Home, "Applications", "ERun.app"), "dev")
	writeDesktopAppBundle(t, filepath.Join(systemApplications, "ERun.app"), "1.0.51")
	envVars := append(setup.Env(),
		"ERUN_HOST_OS_OVERRIDE=darwin",
		"ERUN_DESKTOP_APP_SYSTEM_APPLICATIONS_DIR_OVERRIDE="+systemApplications,
	)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_reports_shadowed_desktop_app_bundle", normalize.Apply(result.Combined))
}

func testDoctorDryRunNoDesktopAppBundleReportsNothing(t *testing.T) {
	// The common case (this host's HOST_OS_OVERRIDE isn't even darwin, and
	// even on darwin, no bundle installed anywhere): no "== Desktop app =="
	// section at all.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "Desktop app") {
		t.Fatalf("expected no desktop-app finding when nothing is installed, got:\n%s", result.Combined)
	}
	golden.Equal(t, "doctor/dry_run_no_desktop_app_bundle_reports_nothing", normalize.Apply(result.Combined))
}

// writeDesktopAppBundle stages the minimal shape of a macOS ERun.app bundle
// (Contents/Info.plist carrying CFBundleShortVersionString) that
// reportInstalledDesktopAppVersion reads, at bundlePath.
func writeDesktopAppBundle(t *testing.T, bundlePath, version string) {
	t.Helper()
	plist := "<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
		"<plist version=\"1.0\">\n" +
		"  <dict>\n" +
		"    <key>CFBundleShortVersionString</key>\n" +
		"    <string>" + version + "</string>\n" +
		"  </dict>\n" +
		"</plist>\n"
	mustWriteFile(t, filepath.Join(bundlePath, "Contents", "Info.plist"), plist)
}
