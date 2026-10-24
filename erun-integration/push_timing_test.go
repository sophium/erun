package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
)

func pushStepTimingScenarios(t *testing.T) {
	// The two scenarios below cover step timing: a push reports where its time
	// went, per component and per architecture, on success and on failure,
	// plus a JSON record alongside the trace. Before that change, `push`
	// reported only a single `==> Pushed in <ELAPSED>` (or `Push failed after
	// <ELAPSED>`) line — no per-component/per-architecture breakdown, no
	// cache-hit/miss duration, and no machine-readable record at all — so a
	// slow or failed push could never be attributed to a cause. The table's
	// per-step durations are real, sub-second wall-clock measurements even
	// under an instant stub, so they cannot be locked into a byte-exact golden
	// the way the rounded `<ELAPSED>` marker in the existing `==> ...` lines
	// can (erun-integration/AGENTS.md § "Whole-output snapshots vs targeted
	// substring assertions", case 3); these scenarios assert on structure and
	// presence instead.
	t.Run("real_run_reports_step_timing_table_and_json_record", testPushRealRunReportsStepTimingTableAndJsonRecord)
	t.Run("real_run_failure_still_reports_step_timing", testPushRealRunFailureStillReportsStepTiming)
}

func testPushRealRunReportsStepTimingTableAndJsonRecord(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	// #1201: give the registry-credential preflight a resolvable credential
	// so this scenario still reaches the step-timing behavior it is about.
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "step timing (ordered by duration):") {
		t.Fatalf("expected a step-timing table in the output, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "api (cache miss") || !strings.Contains(result.Combined, "base (cache miss") {
		t.Fatalf("expected per-component timing rows for api and base, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "linux/amd64 (cache miss") || !strings.Contains(result.Combined, "linux/arm64 (cache miss") {
		t.Fatalf("expected per-architecture timing rows, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "cache miss:") {
		t.Fatalf("expected a cache-miss annotation on the rebuilt images, got:\n%s", result.Combined)
	}

	assertPushTimingRecordCacheMisses(t, setup.Home)
}

func assertPushTimingRecordCacheMisses(t *testing.T, home string) {
	t.Helper()
	record := readTimingRecord(t, home, "push")
	if record.Command != "push" || record.Failed {
		t.Fatalf("unexpected timing record: %+v", record)
	}
	for _, name := range []string{"api", "base"} {
		step := findTimingRecordStep(t, record.Steps, name)
		if step.CacheHit == nil || *step.CacheHit {
			t.Fatalf("expected component %s to record a cache miss, got %+v", name, step)
		}
		if len(step.Steps) < 2 {
			t.Fatalf("expected per-architecture children for %s, got %+v", name, step)
		}
	}
}

func testPushRealRunFailureStillReportsStepTiming(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  build) exit 7 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit when the docker build fails, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "step timing (ordered by duration):") {
		t.Fatalf("a failed push must still report where its time went, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "(failed)") {
		t.Fatalf("expected a (failed) marker in the timing table, got:\n%s", result.Combined)
	}

	record := readTimingRecord(t, setup.Home, "push")
	if !record.Failed || record.Error == "" {
		t.Fatalf("expected the timing record to carry the failure, got %+v", record)
	}
}

// findTimingRecordStep locates a named step among a record's direct children,
// failing the test if it is missing (rather than returning a zero value that
// would let a nil-CacheHit assertion pass for the wrong reason).
func findTimingRecordStep(t testing.TB, steps []timingRecordStep, name string) timingRecordStep {
	t.Helper()
	for _, step := range steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("expected a %q step in the timing record, got %+v", name, steps)
	return timingRecordStep{}
}

// readTimingRecord finds the single `<command>-*.json` file the run just
// wrote under the scenario's isolated $HOME/.erun/timing (timing.go's
// timingRecordDir) and decodes it.
func readTimingRecord(t testing.TB, home, command string) timingRecord {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(home, ".erun", "timing", command+"-*.json"))
	if err != nil {
		t.Fatalf("glob timing records: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one %s timing record under %s, got %v", command, home, matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read timing record %s: %v", matches[0], err)
	}
	var record timingRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("unmarshal timing record %s: %v", matches[0], err)
	}
	return record
}

type timingRecord struct {
	Command string             `json:"command"`
	Failed  bool               `json:"failed"`
	Error   string             `json:"error"`
	Steps   []timingRecordStep `json:"steps"`
}

// timingRecordStep/timingRecord mirror the shape of eruncommon.TimingStepJSON/
// TimingRecord (timing.go) closely enough for these tests to assert on
// structure without importing erun-common's internal types.
type timingRecordStep struct {
	Name     string             `json:"name"`
	Failed   bool               `json:"failed"`
	CacheHit *bool              `json:"cacheHit"`
	Steps    []timingRecordStep `json:"steps"`
}
