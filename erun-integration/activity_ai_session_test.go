package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func reportAISession(t *testing.T, setup env.Setup, args ...string) erun.Result {
	t.Helper()
	full := append([]string{"activity", "ai-session", "report", "--tenant", "team", "--environment", "dev"}, args...)
	result := erun.Run(t, full, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("report %v: exit %d: %s", args, result.ExitCode, result.Combined)
	}
	return result
}

func aiSessionStatusJSON(t *testing.T, setup env.Setup, args ...string) []map[string]any {
	t.Helper()
	full := append([]string{"activity", "ai-session", "status", "--tenant", "team", "--environment", "dev", "--json"}, args...)
	result := erun.Run(t, full, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("status %v: exit %d: %s", args, result.ExitCode, result.Combined)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &rows); err != nil {
		t.Fatalf("parse status --json: %v\n%s", err, result.Stdout)
	}
	return rows
}

// TestActivityAISession: hook-reported status must distinguish awaiting input
// from idle or exited, which output volume cannot.
func TestActivityAISession(t *testing.T) {
	t.Parallel()

	t.Run("awaiting_input_survives_silence_and_is_not_idle_or_exited", testActivityAISessionAwaitingInputSurvivesSilenceAndIsNotIdleOrExited)

	t.Run("notify_also_resolves_to_awaiting_input", testActivityAISessionNotifyAlsoResolvesToAwaitingInput)

	t.Run("exit_with_oom_reason_reports_oom_killed_distinct_from_plain_exit", testActivityAISessionExitWithOOMReasonReportsOOMKilledDistinctFromPlainExit)

	t.Run("status_lists_every_recorded_session_when_none_named", testActivityAISessionStatusListsEveryRecordedSessionWhenNoneNamed)

	t.Run("status_json_reports_empty_array_when_none_recorded", testActivityAISessionStatusJSONReportsEmptyArrayWhenNoneRecorded)

	t.Run("report_rejects_unsupported_event", testActivityAISessionReportRejectsUnsupportedEvent)
}

func testActivityAISessionAwaitingInputSurvivesSilenceAndIsNotIdleOrExited(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")

	reportAISession(t, setup, "--session", "sess-1", "--tool", "claude", "--event", "turn-start")
	busy := aiSessionStatusJSON(t, setup, "--session", "sess-1")
	if busy[0]["state"] != "busy" {
		t.Fatalf("after turn-start: want busy, got %v", busy[0]["state"])
	}

	// No further output arrives, yet this must not read as idle.
	reportAISession(t, setup, "--session", "sess-1", "--event", "turn-end")
	awaiting := aiSessionStatusJSON(t, setup, "--session", "sess-1")
	if awaiting[0]["state"] != "awaiting-input" {
		t.Fatalf("after turn-end with no further output: want awaiting-input, got %v", awaiting[0]["state"])
	}

	neverStarted := aiSessionStatusJSON(t, setup, "--session", "never-started")
	if neverStarted[0]["state"] != "idle" {
		t.Fatalf("session with no recorded event: want idle, got %v", neverStarted[0]["state"])
	}
	if awaiting[0]["state"] == neverStarted[0]["state"] {
		t.Fatalf("awaiting-input must not collapse into idle")
	}

	reportAISession(t, setup, "--session", "sess-1", "--event", "exit", "--exit-code", "0")
	exited := aiSessionStatusJSON(t, setup, "--session", "sess-1")
	if exited[0]["state"] != "exited" {
		t.Fatalf("after exit: want exited, got %v", exited[0]["state"])
	}
}

func testActivityAISessionNotifyAlsoResolvesToAwaitingInput(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	reportAISession(t, setup, "--session", "sess-2", "--tool", "codex", "--event", "notify")
	status := aiSessionStatusJSON(t, setup, "--session", "sess-2")
	if status[0]["state"] != "awaiting-input" {
		t.Fatalf("after notify: want awaiting-input, got %v", status[0]["state"])
	}
}

func testActivityAISessionExitWithOOMReasonReportsOOMKilledDistinctFromPlainExit(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	reportAISession(t, setup, "--session", "oom-session", "--event", "exit", "--exit-code", "137", "--exit-reason", "oom")
	reportAISession(t, setup, "--session", "plain-session", "--event", "exit", "--exit-code", "1")
	oom := aiSessionStatusJSON(t, setup, "--session", "oom-session")
	plain := aiSessionStatusJSON(t, setup, "--session", "plain-session")
	if oom[0]["state"] != "oom-killed" {
		t.Fatalf("exit with oom reason: want oom-killed, got %v", oom[0]["state"])
	}
	if plain[0]["state"] != "exited" {
		t.Fatalf("plain exit: want exited, got %v", plain[0]["state"])
	}
	if oom[0]["state"] == plain[0]["state"] {
		t.Fatalf("an OOM kill must be distinguishable from an ordinary exit")
	}
}

func testActivityAISessionStatusListsEveryRecordedSessionWhenNoneNamed(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	reportAISession(t, setup, "--session", "b-session", "--tool", "codex", "--event", "turn-start")
	reportAISession(t, setup, "--session", "a-session", "--tool", "claude", "--event", "turn-end")
	rows := aiSessionStatusJSON(t, setup)
	if len(rows) != 2 {
		t.Fatalf("want 2 sessions listed, got %d: %v", len(rows), rows)
	}
	if rows[0]["sessionId"] != "a-session" || rows[1]["sessionId"] != "b-session" {
		t.Fatalf("expected sessions sorted by id, got %v", rows)
	}
}

// json.Unmarshal decodes "null" as nil, so this asserts the raw "[]" text.
func testActivityAISessionStatusJSONReportsEmptyArrayWhenNoneRecorded(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "ai-session", "status", "--tenant", "team", "--environment", "dev", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("status --json on an untouched environment: exit %d: %s", result.ExitCode, result.Combined)
	}
	if got := strings.TrimSpace(result.Stdout); got != "[]" {
		t.Fatalf("want status --json to print [] for no recorded sessions, got %q", got)
	}
}

func testActivityAISessionReportRejectsUnsupportedEvent(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "ai-session", "report", "--tenant", "team", "--environment", "dev", "--session", "sess-1", "--event", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unsupported event, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/ai_session_report_rejects_unsupported_event", normalize.Apply(result.Combined))
}
