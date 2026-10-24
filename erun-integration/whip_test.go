package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestWhip(t *testing.T) {
	t.Run("help", testWhipHelp)
	t.Run("dry_run_no_targets_configured", testWhipDryRunNoTargetsConfigured)
	t.Run("dry_run_one_named_environment_not_open_reports_call_failed", testWhipDryRunOneNamedEnvironmentNotOpenReportsCallFailed)
	t.Run("dry_run_scoped_environment_excludes_orchestrators", testWhipDryRunScopedEnvironmentExcludesOrchestrators)
	t.Run("dry_run_whips_every_configured_environment_and_orchestrator", testWhipDryRunWhipsEveryConfiguredEnvironmentAndOrchestrator)
	t.Run("dry_run_json_output", testWhipDryRunJsonOutput)
	t.Run("real_run_forwards_the_resolved_target", testWhipRealRunForwardsTheResolvedTarget)
	t.Run("sweep_continues_past_an_unreachable_channel_and_names_it", testWhipSweepContinuesPastAnUnreachableChannelAndNamesIt)
	t.Run("dry_run_never_reattaches_an_unreachable_channel", testWhipDryRunNeverReattachesAnUnreachableChannel)
	t.Run("dry_run_only_tenant_given_refuses", testWhipDryRunOnlyTenantGivenRefuses)
}

func testWhipHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"whip", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "whip/help", normalize.Apply(result.Combined))
}

func testWhipDryRunNoTargetsConfigured(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"whip", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "whip/dry_run_no_targets_configured", normalize.Apply(result.Combined))
}

func testWhipDryRunOneNamedEnvironmentNotOpenReportsCallFailed(t *testing.T) {
	// Pinned high so the probe cannot hit a live session's default ports. Without a desktop
	// bearer the call fails, which must report as a call failure, never as "not alive".
	skipIfPortsBusy(t, 26100)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	result := erun.Run(t, []string{"whip", "--tenant", "team", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "whip/dry_run_one_named_environment_not_open_reports_call_failed", normalize.Apply(result.Combined))
}

func testWhipDryRunScopedEnvironmentExcludesOrchestrators(t *testing.T) {
	// A scoped call names only that environment, no orchestrator line.
	skipIfPortsBusy(t, 26100)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	seedOrchestrator(t, setup, "eng-1", "Eng One")
	result := erun.Run(t, []string{"whip", "--tenant", "team", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "whip/dry_run_scoped_environment_excludes_orchestrators", normalize.Apply(result.Combined))
}

func testWhipDryRunWhipsEveryConfiguredEnvironmentAndOrchestrator(t *testing.T) {
	// Orchestrators are always unreachable from the CLI: it has no channel to a desktop PTY.
	skipIfPortsBusy(t, 26100, 26200)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "other", "staging", 26200)
	seedOrchestrator(t, setup, "eng-1", "Eng One")
	result := erun.Run(t, []string{"whip", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "whip/dry_run_whips_every_configured_environment_and_orchestrator", normalize.Apply(result.Combined))
}

func testWhipDryRunJsonOutput(t *testing.T) {
	skipIfPortsBusy(t, 26100)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	seedOrchestrator(t, setup, "eng-1", "Eng One")
	result := erun.Run(t, []string{"whip", "--dry-run", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "whip/dry_run_json_output", normalize.Apply(result.Combined))
}

func testWhipRealRunForwardsTheResolvedTarget(t *testing.T) {
	// The whip call must restate the tenant/environment rather than rely on the pod's default.
	whipEdgeLocalPort := 26500
	skipIfPortsBusy(t, whipEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", whipEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"whip"}],` +
		`"structuredContent":{"candidate":{"Kind":"environment","ID":"team/dev","Name":"team/dev","Reachable":true,"Alive":true},"decision":1,"reason":"nudge","pushed":true}}`}}
	edge.start(t, whipEdgeLocalPort)

	result := erun.Run(t, []string{"whip", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	call := edge.requestFor(t, "tools/call")
	if call.Tool != "whip" {
		t.Fatalf("the environment was asked for %q, want whip", call.Tool)
	}
	assertToolArgumentsNameTarget(t, call, "team", "dev")
}

func testWhipSweepContinuesPastAnUnreachableChannelAndNamesIt(t *testing.T) {
	// One dead edge must cost only its own row. Local port ranges align to 100 from 17000,
	// so they cannot be OS-assigned.
	reachablePort, unreachablePort := 26300, 26400
	skipIfPortsBusy(t, reachablePort, unreachablePort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", reachablePort)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "gone", unreachablePort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"whip"}],` +
		`"structuredContent":{"candidate":{"Kind":"environment","ID":"team/dev","Name":"team/dev","Reachable":true,"Alive":true},"decision":1,"reason":"nudge","pushed":false}}`}}
	edge.start(t, reachablePort)

	result := erun.Run(t, []string{"whip", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "team/dev: would push (dry-run)") {
		t.Fatalf("the reachable target was not reported on:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "team/gone: skipped — channel-down:") {
		t.Fatalf("the unreachable target was not named as a skipped channel:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "run `erun open team gone`") {
		t.Fatalf("the unreachable target carried no next action:\n%s", result.Combined)
	}
}

func testWhipDryRunNeverReattachesAnUnreachableChannel(t *testing.T) {
	// The reconnect child cannot be observed without racing, so assert the spawn decision
	// via its trace.
	unreachablePort := 26800
	skipIfPortsBusy(t, unreachablePort)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", unreachablePort)
	fixture.SeedDesktopIdentity(t, setup)

	result := erun.Run(t, []string{"whip", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "reattaching with `erun open --reconnect`") {
		t.Fatalf("a dry run reattached the channel:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "team/dev: skipped — channel-down:") {
		t.Fatalf("the dry run produced no row for the unreachable target:\n%s", result.Combined)
	}
}

func testWhipDryRunOnlyTenantGivenRefuses(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"whip", "--tenant", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with only --tenant given, got 0: %s", result.Combined)
	}
	golden.Equal(t, "whip/dry_run_only_tenant_given_refuses", normalize.Apply(result.Combined))
}

// seedOrchestrator writes what the desktop would to the root config.
func seedOrchestrator(t testing.TB, setup env.Setup, id, name string) {
	t.Helper()
	path := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read root config %s: %v", path, err)
	}
	extra := "orchestrators:\n  - id: " + id + "\n    name: " + name + "\n"
	if err := os.WriteFile(path, append(existing, []byte(extra)...), 0o644); err != nil {
		t.Fatalf("write root config %s: %v", path, err)
	}
}
