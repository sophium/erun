package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// TestJobs covers `erun jobs list`/`show`/`start`/`finish`: the platform's
// record of work in flight, claimed before the work starts rather than
// reported only once it finishes. It is the host-side counterpart to the
// environment-scoped `erun exec job` verbs covered in job_test.go.
func TestJobs(t *testing.T) {
	t.Parallel()
	t.Run("help", testJobsHelp)
	t.Run("list_help", testJobsListHelp)
	t.Run("show_help", testJobsShowHelp)
	t.Run("start_help", testJobsStartHelp)
	t.Run("finish_help", testJobsFinishHelp)
	t.Run("list_dry_run_names_every_filter", testJobsListDryRunNamesEveryFilter)
	t.Run("list_unknown_status_is_refused_as_a_bad_argument", testJobsListUnknownStatusIsRefusedAsABadArgument)
	t.Run("list_without_an_alias_names_the_erun_platform", testJobsListWithoutAnAliasNamesTheErunPlatform)
	t.Run("list_with_a_valid_status_and_no_matches_is_an_empty_result", testJobsListWithAValidStatusAndNoMatchesIsAnEmptyResult)
	t.Run("list_renders_a_running_and_a_finished_job", testJobsListRendersARunningAndAFinishedJob)
	t.Run("show_dry_run", testJobsShowDryRun)
	t.Run("show_requires_a_job_id", testJobsShowRequiresAJobID)
	t.Run("show_renders_the_full_job", testJobsShowRendersTheFullJob)
	t.Run("start_dry_run", testJobsStartDryRun)
	t.Run("start_refuses_an_unknown_type", testJobsStartRefusesAnUnknownType)
	t.Run("start_refuses_an_unknown_actor_kind", testJobsStartRefusesAnUnknownActorKind)
	t.Run("start_requires_a_summary", testJobsStartRequiresASummary)
	t.Run("start_requires_an_actor", testJobsStartRequiresAnActor)
	t.Run("start_records_the_job", testJobsStartRecordsTheJob)
	t.Run("start_with_a_held_scope_names_the_holder", testJobsStartWithAHeldScopeNamesTheHolder)
	t.Run("start_resolves_the_named_environment", testJobsStartResolvesTheNamedEnvironment)
	t.Run("start_with_an_unregistered_environment_names_the_remedy", testJobsStartWithAnUnregisteredEnvironmentNamesTheRemedy)
	t.Run("finish_dry_run", testJobsFinishDryRun)
	t.Run("finish_requires_a_job_id", testJobsFinishRequiresAJobID)
	t.Run("finish_passes_running_to_the_platform", testJobsFinishPassesRunningToThePlatform)
	t.Run("start_planned_dry_run_names_the_status", testJobsStartPlannedDryRunNamesTheStatus)
	t.Run("finish_unknown_status_is_refused_as_a_bad_argument", testJobsFinishUnknownStatusIsRefusedAsABadArgument)
	t.Run("finish_closes_the_job", testJobsFinishClosesTheJob)
	t.Run("list_json_is_the_wire_shape", testJobsListJSONIsTheWireShape)
}

func testJobsHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/help", normalize.Apply(result.Combined))
}

func testJobsListHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/list_help", normalize.Apply(result.Combined))
}

func testJobsShowHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "show", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/show_help", normalize.Apply(result.Combined))
}

func testJobsStartHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "start", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/start_help", normalize.Apply(result.Combined))
}

func testJobsFinishHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "finish", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/finish_help", normalize.Apply(result.Combined))
}

// Every filter is a distinct dimension of the queue read, so the trace
// has to name each resolved value rather than letting a silently dropped
// filter read as "the platform returned nothing".
func testJobsListDryRunNamesEveryFilter(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "list",
		"--status", "running",
		"--environment-id", "env_01HQ",
		"--issue", "sophium/erun#1",
		"--scope", "sophium/erun#1",
		"--actor", "erun/code2",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/list_dry_run_names_every_filter", normalize.Apply(result.Combined))
}

func testJobsListUnknownStatusIsRefusedAsABadArgument(t *testing.T) {
	// A mistyped --status must fail naming the accepted values. Passing it
	// through to the platform filter would come back as an empty listing
	// -- "no jobs" and exit 0 -- indistinguishable from a real empty queue.
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"jobs", "list", "--status", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 for an unrecognised --status, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/list_unknown_status_is_refused_as_a_bad_argument", normalize.Apply(result.Combined))
}

func testJobsListWithoutAnAliasNamesTheErunPlatform(t *testing.T) {
	// The counterpart to job_report.go's silent contract: a caller that
	// asked for this specific read is told it could not be resolved,
	// rather than being shown an empty queue it would read as real.
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 with no erun platform alias configured, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/list_without_an_alias_names_the_erun_platform", normalize.Apply(result.Combined))
}

func testJobsListWithAValidStatusAndNoMatchesIsAnEmptyResult(t *testing.T) {
	setup := env.New(t)
	server := jobListStubServer(t, nil)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"jobs", "list", "--status", "FAILED"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d for a valid --status with no matches, want 0:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/list_with_a_valid_status_and_no_matches_is_an_empty_result",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

func testJobsListRendersARunningAndAFinishedJob(t *testing.T) {
	// The row's whole point is the prose summary, the actor and the claim
	// -- so a run must show a job that claims a scope and names its issue
	// beside one that claims nothing.
	setup := env.New(t)
	server := jobListStubServer(t, []map[string]any{
		platformJobJSON(map[string]any{
			"jobId":    "job_01HQ_running",
			"summary":  "fixing the jobs claim race",
			"scope":    "sophium/erun#1",
			"issueRef": "sophium/erun#1",
		}),
		platformJobJSON(map[string]any{
			"jobId":   "job_01HQ_done",
			"status":  "SUCCEEDED",
			"summary": "gate passed for the jobs surface",
			"endedAt": "2026-01-02T04:05:06Z",
		}),
	})
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"jobs", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/list_renders_a_running_and_a_finished_job",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

func testJobsShowDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"jobs", "show", "job_01HQ", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/show_dry_run", normalize.Apply(result.Combined))
}

func testJobsShowRequiresAJobID(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "show"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 with no job id, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/show_requires_a_job_id", normalize.Apply(result.Combined))
}

func testJobsShowRendersTheFullJob(t *testing.T) {
	setup := env.New(t)
	server := jobsAPIServer(t, "", nil)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"jobs", "show", "job_01HQ"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/show_renders_the_full_job",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

func testJobsStartDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "start",
		"--type", "fix",
		"--issue", "sophium/erun#1",
		"--scope", "sophium/erun#1",
		"--summary", "fixing the jobs claim race",
		"--actor", "erun/code2",
		"--local-job-id", "job-local-1",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/start_dry_run", normalize.Apply(result.Combined))
}

// The three vocabularies below are closed for the same reason: the
// dashboard groups by them, so a value outside the set is a row it cannot
// render. Each is refused up front rather than stored.
func testJobsStartRefusesAnUnknownType(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "start", "--type", "bogus", "--summary", "work", "--actor", "erun/code2",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 for an unrecognised --type, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/start_refuses_an_unknown_type", normalize.Apply(result.Combined))
}

func testJobsStartRefusesAnUnknownActorKind(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "start", "--type", "fix", "--summary", "work", "--actor", "erun/code2",
		"--actor-kind", "robot",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 for an unrecognised --actor-kind, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/start_refuses_an_unknown_actor_kind", normalize.Apply(result.Combined))
}

func testJobsStartRequiresASummary(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "start", "--type", "fix", "--actor", "erun/code2",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 with no --summary, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/start_requires_a_summary", normalize.Apply(result.Combined))
}

func testJobsStartRequiresAnActor(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "start", "--type", "fix", "--summary", "work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 with no --actor, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/start_requires_an_actor", normalize.Apply(result.Combined))
}

func testJobsStartRecordsTheJob(t *testing.T) {
	setup := env.New(t)
	server := jobsAPIServer(t, "", nil)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{
		"jobs", "start",
		"--type", "fix",
		"--summary", "fixing the jobs claim race",
		"--actor", "erun/code2",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/start_records_the_job",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

// A refused claim is the coordination primitive's whole payload: the
// caller has to be told who holds the scope, what they are doing and since
// when, or "conflict" teaches it nothing it can act on.
func testJobsStartWithAHeldScopeNamesTheHolder(t *testing.T) {
	setup := env.New(t)
	server := jobsAPIServer(t, "sophium/erun#1", nil)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{
		"jobs", "start",
		"--type", "fix",
		"--summary", "fixing the jobs claim race",
		"--actor", "erun/code2",
		"--scope", "sophium/erun#1",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 for a claim on a scope another job holds, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/start_with_a_held_scope_names_the_holder",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

// A --environment names a local environment, so the request carries the
// platform's own id for it. Work that never enters an environment is the
// real case this resolves to "".
func testJobsStartResolvesTheNamedEnvironment(t *testing.T) {
	setup := env.New(t)
	server := jobsAPIServer(t, "", []map[string]any{
		{"environmentId": "env_01HQ", "name": "code2", "type": "local-agent", "status": "ready"},
	})
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{
		"jobs", "start",
		"--type", "gate",
		"--summary", "running the integration gate",
		"--actor", "erun/code2",
		"--environment", "code2",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/start_resolves_the_named_environment",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

func testJobsStartWithAnUnregisteredEnvironmentNamesTheRemedy(t *testing.T) {
	setup := env.New(t)
	server := jobsAPIServer(t, "", nil)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{
		"jobs", "start",
		"--type", "gate",
		"--summary", "running the integration gate",
		"--actor", "erun/code2",
		"--environment", "not-registered",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 for an environment the platform does not know, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/start_with_an_unregistered_environment_names_the_remedy",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

func testJobsFinishDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "finish", "job_01HQ", "--status", "succeeded", "--summary", "gate passed",
		"--local-job-id", "job-local-1", "--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/finish_dry_run", normalize.Apply(result.Combined))
}

func testJobsFinishRequiresAJobID(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"jobs", "finish"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 with no job id, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/finish_requires_a_job_id", normalize.Apply(result.Combined))
}

// RUNNING on a finish is no longer refused locally. It means exactly one
// thing -- the PLANNED job this names has begun -- and which moves are
// legal from a job's current status is the platform's to decide: it
// accepts RUNNING for a plan, treats it as a no-op on a running job, and
// refuses it on a finished one. Restating that as a local guess about a
// status this layer cannot read is what would drift.
func testJobsFinishPassesRunningToThePlatform(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "finish", "job_01HQ", "--status", "RUNNING",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 with no signed-in platform, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/finish_passes_running_to_the_platform", normalize.Apply(result.Combined))
}

// PLANNED is the rung the whole pipeline view is built on: work recorded
// against its issue before any code exists. The trace has to carry the
// status it is recording, or a plan silently becomes a claim that
// something is already running.
func testJobsStartPlannedDryRunNamesTheStatus(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "start",
		"--type", "triage",
		"--status", "planned",
		"--issue", "sophium/erun#1",
		"--scope", "sophium/erun#1",
		"--summary", "planning the jobs status ladder",
		"--actor", "erun/ideas",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/start_planned_dry_run_names_the_status", normalize.Apply(result.Combined))
}

func testJobsFinishUnknownStatusIsRefusedAsABadArgument(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"jobs", "finish", "job_01HQ", "--status", "bogus",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("exit 0 for an unrecognised --status, want a non-zero exit:\n%s", result.Combined)
	}
	golden.Equal(t, "jobs/finish_unknown_status_is_refused_as_a_bad_argument", normalize.Apply(result.Combined))
}

func testJobsFinishClosesTheJob(t *testing.T) {
	setup := env.New(t)
	server := jobsAPIServer(t, "", nil)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{
		"jobs", "finish", "job_01HQ", "--status", "SUCCEEDED", "--summary", "gate passed",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/finish_closes_the_job",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}

// The JSON shape is the contract the dashboard and the MCP tools both
// read, so one scenario asserts it directly rather than only through the
// human-facing rendering.
func testJobsListJSONIsTheWireShape(t *testing.T) {
	setup := env.New(t)
	server := jobListStubServer(t, []map[string]any{
		platformJobJSON(map[string]any{"jobId": "job_01HQ_running"}),
	})
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"jobs", "list", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "jobs/list_json_is_the_wire_shape",
		normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
}
