package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func mustWriteFile(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustReadFile(t testing.TB, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func captureGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := harnessexec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// execDangerousContent holds shell constructs that must round-trip as data, never shell-parsed.
const execDangerousContent = "line one\n`echo pwned` $(echo pwned) \"quoted\" 'quoted'\ntrailing\n\n"

var execScenarios = []struct {
	name string
	run  func(*testing.T)
}{
	{"help", testExecHelp},
	{"noop_dry_run", testExecNoopDryRun},
	{"raw_dry_run", testExecRawDryRun},
	{"raw_help", testExecRawHelp},
	{"raw_dry_run_traces_inside_project", testExecRawDryRunTracesInsideProject},
	{"raw_dry_run_redacts_sensitive_args", testExecRawDryRunRedactsSensitiveArgs},
	{"raw_dry_run_double_dash_passes_flags_through", testExecRawDryRunDoubleDashPassesFlagsThrough},
	{"diff_dry_run_traces_git_diff", testExecDiffDryRunTracesGitDiff},
	{"diff_dry_run_errors_outside_git_project", testExecDiffDryRunErrorsOutsideGitProject},
	{"diff_json_emits_structured_result", testExecDiffJsonEmitsStructuredResult},
	{"diff_includes_untracked_files", testExecDiffIncludesUntrackedFiles},
	{"diff_files_match_tree_order", testExecDiffFilesMatchTreeOrder},
	{"diff_scope_all_traces_review_base", testExecDiffScopeAllTracesReviewBase},
	{"diff_scope_current_committed_change_still_reports_review_commits", testExecDiffScopeCurrentCommittedChangeStillReportsReviewCommits},
	{"diff_scope_current_default_includes_staged_changes", testExecDiffScopeCurrentDefaultIncludesStagedChanges},
	{"diff_scope_current_explicit_matches_default_for_staged_changes", testExecDiffScopeCurrentExplicitMatchesDefaultForStagedChanges},
	{"diff_scope_current_no_commits_yet_still_diffs_staged_changes", testExecDiffScopeCurrentNoCommitsYetStillDiffsStagedChanges},
	{"diff_scope_commit_uses_selected_commit_parent", testExecDiffScopeCommitUsesSelectedCommitParent},
	{"diff_parses_deleted_and_binary_files", testExecDiffParsesDeletedAndBinaryFiles},
	{"diff_scope_all_resolves_origin_head_and_renames", testExecDiffScopeAllResolvesOriginHeadAndRenames},
	{"diff_raw_output_includes_deletions", testExecDiffRawOutputIncludesDeletions},
	{"diff_selected_commit_without_scope_errors", testExecDiffSelectedCommitWithoutScopeErrors},
	{"diff_selected_commit_option_shaped_refused", testExecDiffSelectedCommitOptionShapedRefused},
	{"diff_selected_commit_malformed_revision_errors", testExecDiffSelectedCommitMalformedRevisionErrors},
	{"diff_scope_commit_resolves_abbreviated_commit", testExecDiffScopeCommitResolvesAbbreviatedCommit},
	{"dry_run_with_time_flag_prints_elapsed_on_error", testExecDryRunWithTimeFlagPrintsElapsedOnError},
	{"write_help", testExecWriteHelp},
	{"write_dry_run_traces_resolved_path", testExecWriteDryRunTracesResolvedPath},
	{"write_dry_run_errors_outside_git_project", testExecWriteDryRunErrorsOutsideGitProject},
	{"write_refuses_path_outside_project_root", testExecWriteRefusesPathOutsideProjectRoot},
	{"write_refuses_symlink_leaf_pointing_outside_tree", testExecWriteRefusesSymlinkLeafPointingOutsideTree},
	{"write_refuses_write_through_symlinked_directory_component", testExecWriteRefusesWriteThroughSymlinkedDirectoryComponent},
	{"write_root_itself_symlink_still_writes_inside_real_tree", testExecWriteRootItselfSymlinkStillWritesInsideRealTree},
	{"write_refuses_absolute_path_outside_root", testExecWriteRefusesAbsolutePathOutsideRoot},
	{"write_refuses_path_outside_root_after_dotdot_normalisation", testExecWriteRefusesPathOutsideRootAfterDotdotNormalisation},
	{"write_content_round_trips_byte_identical_with_dangerous_shell_constructs", testExecWriteContentRoundTripsByteIdenticalWithDangerousShellConstructs},
	{"commit_help", testExecCommitHelp},
	{"commit_dry_run_verifies_branch_and_traces", testExecCommitDryRunVerifiesBranchAndTraces},
	{"commit_dry_run_refuses_branch_mismatch", testExecCommitDryRunRefusesBranchMismatch},
	{"commit_refuses_branch_mismatch", testExecCommitRefusesBranchMismatch},
	{"commit_commits_dangerous_message_and_reports_result", testExecCommitCommitsDangerousMessageAndReportsResult},
	{"commit_refuses_when_nothing_to_commit", testExecCommitRefusesWhenNothingToCommit},
	{"commit_scoped_paths_refuses_unrelated_dirty_file", testExecCommitScopedPathsRefusesUnrelatedDirtyFile},
	{"commit_scoped_paths_commits_only_the_declared_file", testExecCommitScopedPathsCommitsOnlyTheDeclaredFile},
	{"commit_dry_run_scoped_paths_traces_neutralized_add_args", testExecCommitDryRunScopedPathsTracesNeutralizedAddArgs},
	{"commit_dry_run_names_files_that_would_be_committed", testExecCommitDryRunNamesFilesThatWouldBeCommitted},
	{"commit_dry_run_scoped_paths_refuses_unrelated_dirty_file", testExecCommitDryRunScopedPathsRefusesUnrelatedDirtyFile},
	{"commit_scoped_paths_rejects_blank_path_entries", testExecCommitScopedPathsRejectsBlankPathEntries},
	{"commit_scoped_paths_accepts_directory_scope", testExecCommitScopedPathsAcceptsDirectoryScope},
	{"commit_scoped_paths_accepts_non_ascii_filename", testExecCommitScopedPathsAcceptsNonAsciiFilename},
	{"commit_surfaces_git_add_failure_output", testExecCommitSurfacesGitAddFailureOutput},
	{"commit_surfaces_git_commit_failure_output", testExecCommitSurfacesGitCommitFailureOutput},
	{"commit_reports_the_branch_the_commit_actually_landed_on", testExecCommitReportsTheBranchTheCommitActuallyLandedOn},
	{"commit_root_not_git_toplevel_ignores_changes_outside_root", testExecCommitRootNotGitToplevelIgnoresChangesOutsideRoot},
	{"commit_root_not_git_toplevel_still_refuses_unrelated_change_inside_root", testExecCommitRootNotGitToplevelStillRefusesUnrelatedChangeInsideRoot},
	{"commit_dry_run_traces_neutralized_colon_pathspec_magic", testExecCommitDryRunTracesNeutralizedColonPathspecMagic},
	{"commit_scoped_paths_pathspec_magic_escape_attempt_stays_refused", testExecCommitScopedPathsPathspecMagicEscapeAttemptStaysRefused},
	{"push_help", testExecPushHelp},
	{"push_dry_run_verifies_branch_and_traces", testExecPushDryRunVerifiesBranchAndTraces},
	{"push_dry_run_refuses_branch_mismatch", testExecPushDryRunRefusesBranchMismatch},
	{"push_refuses_branch_mismatch", testExecPushRefusesBranchMismatch},
	{"push_real_run_lands_the_branch_on_the_remote", testExecPushRealRunLandsTheBranchOnTheRemote},
	{"push_dry_run_must_not_reach_the_network", testExecPushDryRunMustNotReachTheNetwork},
	{"merge_help", testExecMergeHelp},
	{"merge_dry_run_must_not_reach_the_network", testExecMergeDryRunMustNotReachTheNetwork},
	{"merge_real_run_merges_the_target_branch", testExecMergeRealRunMergesTheTargetBranch},
	{"merge_conflict_real_run_leaves_the_worktree_mid_merge", testExecMergeConflictRealRunLeavesTheWorktreeMidMerge},
	{"gate_merge_help", testExecGateMergeHelp},
	{"gate_merge_refuses_missing_source", testExecGateMergeRefusesMissingSource},
	{"gate_merge_refuses_missing_target", testExecGateMergeRefusesMissingTarget},
	{"gate_merge_refuses_stdin_message_count_mismatch", testExecGateMergeRefusesStdinMessageCountMismatch},
	{"gate_merge_dry_run_must_not_reach_the_network", testExecGateMergeDryRunMustNotReachTheNetwork},
	{"gate_merge_dry_run_refuses_dirty_worktree", testExecGateMergeDryRunRefusesDirtyWorktree},
	{"gate_merge_dry_run_batch_traces_each_source", testExecGateMergeDryRunBatchTracesEachSource},
	{"gate_merge_refuses_while_the_environment_is_held_exclusively", testExecGateMergeRefusesWhileTheEnvironmentIsHeldExclusively},
	{"gate_merge_refuses_a_claim_taken_at_the_default_scope", testExecGateMergeRefusesAClaimTakenAtTheDefaultScope},
	{"gate_merge_under_lease_is_not_refused_by_the_callers_own_claim", testExecGateMergeUnderLeaseIsNotRefusedByTheCallersOwnClaim},
	{"gate_merge_real_run_squash_merges_onto_target", testExecGateMergeRealRunSquashMergesOntoTarget},
	{"gate_merge_real_run_carries_the_branchs_closes_trailer", testExecGateMergeRealRunCarriesTheBranchsClosesTrailer},
	{"gate_merge_real_run_carries_trailers_past_a_trailing_issue_reference", testExecGateMergeRealRunCarriesTrailersPastATrailingIssueReference},
	{"gate_merge_real_run_carries_a_hard_wrapped_trailer_whole", testExecGateMergeRealRunCarriesAHardWrappedTrailerWhole},
	{"gate_merge_real_run_accepts_a_url_remote", testExecGateMergeRealRunAcceptsAUrlRemote},
	{"gate_merge_real_run_batch_lands_multiple_sources", testExecGateMergeRealRunBatchLandsMultipleSources},
	{"gate_merge_real_run_skips_a_source_that_contributes_nothing", testExecGateMergeRealRunSkipsASourceThatContributesNothing},
	{"gate_merge_real_run_batch_skips_a_conflicting_source_and_lands_the_rest", testExecGateMergeRealRunBatchSkipsAConflictingSourceAndLandsTheRest},
	{"gate_merge_real_run_all_sources_conflict_refuses", testExecGateMergeRealRunAllSourcesConflictRefuses},
	{"gate_merge_real_run_batch_lands_migrations_with_conflicting_atlas_sum", testExecGateMergeRealRunBatchLandsMigrationsWithConflictingAtlasSum},
	{"merge_real_run_regenerates_conflicting_atlas_sum", testExecMergeRealRunRegeneratesConflictingAtlasSum},
	{"report_commit_status_help", testExecReportCommitStatusHelp},
	{"report_commit_status_dry_run_default_context", testExecReportCommitStatusDryRunDefaultContext},
	{"report_commit_status_dry_run_explicit_context_and_target_url", testExecReportCommitStatusDryRunExplicitContextAndTargetUrl},
	{"report_commit_status_dry_run_ssh_remote_url", testExecReportCommitStatusDryRunSshRemoteUrl},
	{"report_commit_status_dry_run_empty_commit_traces_then_refuses", testExecReportCommitStatusDryRunEmptyCommitTracesThenRefuses},
	{"report_commit_status_dry_run_invalid_state_traces_then_refuses", testExecReportCommitStatusDryRunInvalidStateTracesThenRefuses},
	{"report_commit_status_dry_run_missing_description_traces_then_refuses", testExecReportCommitStatusDryRunMissingDescriptionTracesThenRefuses},
	{"report_commit_status_dry_run_malformed_remote_url_traces_then_refuses", testExecReportCommitStatusDryRunMalformedRemoteUrlTracesThenRefuses},
	{"report_commit_status_dry_run_missing_remote_url_traces_then_refuses", testExecReportCommitStatusDryRunMissingRemoteUrlTracesThenRefuses},
	{"report_commit_status_real_run_fails_cleanly_without_a_token", testExecReportCommitStatusRealRunFailsCleanlyWithoutAToken},
	{"report_commit_status_real_run_succeeds", testExecReportCommitStatusRealRunSucceeds},
	{"report_commit_status_real_run_github_rejects", testExecReportCommitStatusRealRunGithubRejects},
	{"close_pr_help", testExecClosePrHelp},
	{"close_pr_dry_run_traces_lookup", testExecClosePrDryRunTracesLookup},
	{"close_pr_dry_run_missing_required_flags_traces_then_refuses", testExecClosePrDryRunMissingRequiredFlagsTracesThenRefuses},
	{"close_pr_dry_run_empty_branch_traces_then_refuses", testExecClosePrDryRunEmptyBranchTracesThenRefuses},
	{"close_pr_dry_run_malformed_remote_url_traces_then_refuses", testExecClosePrDryRunMalformedRemoteUrlTracesThenRefuses},
	{"close_pr_real_run_fails_cleanly_without_a_token", testExecClosePrRealRunFailsCleanlyWithoutAToken},
	{"close_pr_real_run_no_open_pull_request_is_a_no_op", testExecClosePrRealRunNoOpenPullRequestIsANoOp},
	{"close_pr_real_run_comments_and_closes_a_matching_pull_request", testExecClosePrRealRunCommentsAndClosesAMatchingPullRequest},
	{"close_pr_real_run_refuses_when_head_moved", testExecClosePrRealRunRefusesWhenHeadMoved},
	{"reconcile_bypass_help", testExecReconcileBypassHelp},
	{"reconcile_bypass_dry_run", testExecReconcileBypassDryRun},
	{"reconcile_bypass_dry_run_with_since", testExecReconcileBypassDryRunWithSince},
	{"reconcile_bypass_dry_run_missing_ruleset_id_traces_then_refuses", testExecReconcileBypassDryRunMissingRulesetIdTracesThenRefuses},
	{"reconcile_bypass_real_run_fails_cleanly_without_a_token", testExecReconcileBypassRealRunFailsCleanlyWithoutAToken},
}

func TestExec(t *testing.T) {
	t.Parallel()
	for _, scenario := range execScenarios {
		t.Run(scenario.name, scenario.run)
	}
}

func testExecHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/help", normalize.Apply(result.Combined))
}

func testExecNoopDryRun(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "noop", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "exec/noop_dry_run", normalize.Apply(result.Combined))
}

func testExecRawDryRun(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "raw", "echo", "hello", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "exec/raw_dry_run", normalize.Apply(result.Combined))
}

func testExecRawHelp(t *testing.T) {
	// raw disables flag parsing, so --help is intercepted explicitly.
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "raw", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/raw_help", normalize.Apply(result.Combined))
}

func testExecRawDryRunTracesInsideProject(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "raw", "echo", "hello", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "exec/raw_dry_run_traces_inside_project", normalize.Apply(result.Combined))
}

func testExecRawDryRunRedactsSensitiveArgs(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "raw", "--dry-run", "curl", "https://example", "--token", "secret-value", "--password=hidden", "ok"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "exec/raw_dry_run_redacts_sensitive_args", normalize.Apply(result.Combined))
}

func testExecRawDryRunDoubleDashPassesFlagsThrough(t *testing.T) {
	// erun's own flags before -- are consumed; everything after passes through verbatim.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "raw", "--dry-run=true", "--", "echo", "--dry-run", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/raw_dry_run_double_dash_passes_flags_through", normalize.Apply(result.Combined))
}

func testExecDryRunWithTimeFlagPrintsElapsedOnError(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "diff", "--dry-run", "--time"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit (no git project), got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/dry_run_with_time_flag_prints_elapsed_on_error", normalize.Apply(result.Combined))
}

// githubRulesetStubOptions configures a GitHub REST double for reconcile-bypass and plan-ruleset-bypass,
// reached through ERUN_GITHUB_API_BASE_URL_OVERRIDE.
type githubRulesetStubOptions struct {
	RulesetStatus    int
	RuleSuitesStatus int
	// HideBypassActors mimics GitHub's response to a token without write access.
	HideBypassActors bool
	RefInclude       string
	QueuePermission  string
}

func githubRulesetStubServer(t testing.TB, opts githubRulesetStubOptions) *httptest.Server {
	t.Helper()
	if opts.RefInclude == "" {
		opts.RefInclude = `"refs/heads/main"`
	}
	if opts.QueuePermission == "" {
		opts.QueuePermission = "write"
	}
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/sophium/erun/rulesets/rule-suites", func(w http.ResponseWriter, r *http.Request) {
		if opts.RuleSuitesStatus != 0 {
			http.Error(w, `{"message":"Resource not accessible by personal access token"}`, opts.RuleSuitesStatus)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[
				{"id":4,"actor_name":"a-human","before_sha":"before-ungated","after_sha":"ungated-tip","pushed_at":"2026-09-02T09:00:00Z","result":"bypass"}
			]`))
			return
		}
		w.Header().Set("Link", `<`+serverURL+`/repos/sophium/erun/rulesets/rule-suites?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[
			{"id":1,"actor_name":"erun-merge-queue","before_sha":"before-merge","after_sha":"gated-merge-tip","pushed_at":"2026-09-02T12:00:00Z","result":"bypass"},
			{"id":2,"actor_name":"erun-merge-queue","before_sha":"before-release","after_sha":"release-prepare-tip","pushed_at":"2026-09-02T11:00:00Z","result":"bypass"},
			{"id":3,"actor_name":"erun-merge-queue","before_sha":"before-other","after_sha":"other-ruleset-tip","pushed_at":"2026-09-02T10:00:00Z","result":"bypass"}
		]`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/rulesets/rule-suites/{id}", func(w http.ResponseWriter, r *http.Request) {
		rulesetID := "11081432"
		if r.PathValue("id") == "3" {
			rulesetID = "99999999"
		}
		_, _ = w.Write([]byte(`{"rule_evaluations":[
			{"rule_source":{"type":"protected_branch"},"result":"pass","rule_type":"non_fast_forward"},
			{"rule_source":{"type":"ruleset","id":` + rulesetID + `,"name":"main"},"result":"fail","rule_type":"pull_request"},
			{"rule_source":{"type":"ruleset","id":` + rulesetID + `,"name":"main"},"result":"pass","rule_type":"deletion"}
		]}`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/compare/{range}", func(w http.ResponseWriter, r *http.Request) {
		commits := map[string]string{
			"before-release...release-prepare-tip": `[{"sha":"release-commit"},{"sha":"packaging-sync"},{"sha":"release-prepare-tip"}]`,
			"before-ungated...ungated-tip":         `[{"sha":"ungated-tip"}]`,
		}[r.PathValue("range")]
		if commits == "" {
			commits = `[]`
		}
		_, _ = w.Write([]byte(`{"commits":` + commits + `}`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/tags", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"v1.0.247","commit":{"sha":"release-commit"}}]`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/rulesets/11081432", func(w http.ResponseWriter, _ *http.Request) {
		if opts.RulesetStatus != 0 {
			http.Error(w, `{"message":"Not Found"}`, opts.RulesetStatus)
			return
		}
		bypassActors := `,"bypass_actors":[
			{"actor_id":2,"actor_type":"RepositoryRole","bypass_mode":"always"},
			{"actor_id":4,"actor_type":"RepositoryRole","bypass_mode":"always"},
			{"actor_id":5,"actor_type":"RepositoryRole","bypass_mode":"always"}
		]`
		if opts.HideBypassActors {
			bypassActors = ""
		}
		_, _ = w.Write([]byte(`{"id":11081432,"name":"main","target":"branch","enforcement":"active",
			"conditions":{"ref_name":{"include":[` + opts.RefInclude + `],"exclude":[]}},
			"rules":[{"type":"deletion"},{"type":"non_fast_forward"},
				{"type":"pull_request","parameters":{"required_approving_review_count":0}}],
			"current_user_can_bypass":"always"` + bypassActors + `}`))
	})
	mux.HandleFunc("GET /users/{login}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":221100,"login":"` + r.PathValue("login") + `","type":"User"}`))
	})
	mux.HandleFunc("GET /repos/sophium/erun/collaborators/{login}/permission", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"permission":"` + opts.QueuePermission + `"}`))
	})
	// Assign the URL before Start so handler goroutines read it with a happens-before edge.
	server := httptest.NewUnstartedServer(mux)
	serverURL = "http://" + server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	return server
}

// TestGithubRulesetStubServerAssignsItsURLBeforeItServes checks the ordering only under -race, which the gate
// does not run. It needs an out-of-process client (in-process requests are ordered by netpoll) and a second
// stub (the first is silent).
func TestGithubRulesetStubServerAssignsItsURLBeforeItServes(t *testing.T) {
	curl, err := osexec.LookPath("curl")
	if err != nil {
		t.Fatalf("this probe needs curl as an out-of-process HTTP client: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		server := githubRulesetStubServer(t, githubRulesetStubOptions{})
		out, err := harnessexec.Command(curl, "-sS", "-i", server.URL+"/repos/sophium/erun/rulesets/rule-suites").CombinedOutput()
		if err != nil {
			t.Fatalf("request %d: %v\n%s", attempt, err, out)
		}
		if want := "Link: <" + server.URL + "/repos/sophium/erun/rulesets/rule-suites?page=2>"; !strings.Contains(string(out), want) {
			t.Fatalf("request %d: response does not carry %q:\n%s", attempt, want, out)
		}
	}
}

// successfulBuildsStubServer reports a successful build only for the queue's merge commit.
func successfulBuildsStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/builds", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		builds := []map[string]any{}
		if r.URL.Query().Get("commit") == "gated-merge-tip" && r.URL.Query().Get("successful") == "true" {
			builds = append(builds, map[string]any{
				"buildId": "build_1", "tenantId": "tenant-1", "reviewId": "review-1", "successful": true,
				"commitId": "gated-merge-tip", "version": "1.0.248",
				"createdAt": "2026-09-02T12:00:00Z", "updatedAt": "2026-09-02T12:00:00Z",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"builds": builds})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// stubServerRule collapses one stub server's base URL to token, leaving pinned ports elsewhere visible.
func stubServerRule(server *httptest.Server, token string) normalize.Replacement {
	// The default rules have already rewritten the host to <LOOPBACK>.
	normalized := strings.Replace(server.URL, "127.0.0.1", "<LOOPBACK>", 1)
	return normalize.Replacement{Pattern: regexp.MustCompile(regexp.QuoteMeta(normalized)), Token: token}
}

func githubStubEnv(server *httptest.Server) []string {
	return []string{
		"ERUN_GITHUB_API_BASE_URL_OVERRIDE=" + server.URL + "/",
		"GITHUB_TOKEN=gho_stub_token",
	}
}
