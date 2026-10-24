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
	// An image registry different from runtimeregistry may be unpullable. Pure
	// config comparison, so dry-run and real run report alike.
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
	// A stock runtimeimage while runtimerunningimage ran the tenant's own image is
	// config drift, reported alike in dry-run and real run.
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
	// A gitignored project config works on this machine only.
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
	// A bundle beside the CLI binary is the copy `erun app` launches. The
	// executable-dir seam keeps the bundle out of other scenarios.
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
	// Two bundles share one bundle id, so either may launch: both are named and the
	// stale one flagged.
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

// writeDesktopAppBundle stages the Info.plist that reportInstalledDesktopAppVersion reads.
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
