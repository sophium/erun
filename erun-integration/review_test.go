package integration

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// reviewRepository is the repository the review scenarios below open their
// reviews in. A review has to name one — two repositories a tenant serves
// would otherwise share a queue for the same target branch — and scenarios
// that do not exercise the checkout's own origin derive it, name it here so
// the assertion is about the review rather than about the runner's cwd.
const reviewRepository = "https://github.com/sophium/erun"

// otherRepository is a second repository the same tenant serves, which is the
// shape that made a target branch alone an ambiguous name for a queue.
const otherRepository = "https://github.com/sophium/other"

// createReviewJSON runs `review create --output json` against the stub
// server and decodes the resulting reviewId, for scenarios that need a real
// review to act on rather than a --dry-run trace.
func createReviewJSON(t testing.TB, setup env.Setup, name, sourceBranch, targetBranch string) struct{ ReviewID string } {
	t.Helper()
	result := erun.Run(t, []string{
		"review", "create", "--name", name, "--repository", reviewRepository,
		"--source-branch", sourceBranch, "--target-branch", targetBranch, "--output", "json",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("review create exit %d: %s", result.ExitCode, result.Combined)
	}
	var decoded struct{ ReviewID string }
	if err := json.Unmarshal([]byte(result.Stdout), &decoded); err != nil {
		t.Fatalf("decode review create --output json: %v\n%s", err, result.Stdout)
	}
	if decoded.ReviewID == "" {
		t.Fatalf("expected a non-empty reviewId, got:\n%s", result.Stdout)
	}
	return decoded
}

// postCommentJSON runs `review comment --output json` against the stub
// server and decodes the resulting commentId. parentCommentID is passed as
// --reply-to when non-empty, making the posted comment a reply.
func postCommentJSON(t testing.TB, setup env.Setup, reviewID, commitID, filePath string, line int, body, parentCommentID string) string {
	t.Helper()
	args := []string{
		"review", "comment", reviewID, "--commit", commitID, "--file", filePath, "--line", strconv.Itoa(line), "--output", "json",
	}
	if parentCommentID != "" {
		args = append(args, "--reply-to", parentCommentID)
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: body + "\n"})
	if result.ExitCode != 0 {
		t.Fatalf("review comment exit %d: %s", result.ExitCode, result.Combined)
	}
	var decoded struct{ CommentID string }
	if err := json.Unmarshal([]byte(result.Stdout), &decoded); err != nil {
		t.Fatalf("decode review comment --output json: %v\n%s", err, result.Stdout)
	}
	if decoded.CommentID == "" {
		t.Fatalf("expected a non-empty commentId, got:\n%s", result.Stdout)
	}
	return decoded.CommentID
}

func TestReview(t *testing.T) {
	t.Parallel()
	t.Run("help", testReviewHelp)

	t.Run("list_no_alias_configured", testReviewListNoAliasConfigured)

	t.Run("list_dry_run_traces_resolved_call", testReviewListDryRunTracesResolvedCall)

	t.Run("list_mine_dry_run_traces_whoami_and_placeholder_filter", testReviewListMineDryRunTracesWhoamiAndPlaceholderFilter)

	t.Run("list_rejects_mine_combined_with_author_user_id", testReviewListRejectsMineCombinedWithAuthorUserID)

	t.Run("list_mistyped_status_is_refused_rather_than_listed_as_empty", testReviewListMistypedStatusIsRefusedRatherThanListedAsEmpty)

	t.Run("list_lowercase_status_resolves_to_the_stored_spelling", testReviewListLowercaseStatusResolvesToTheStoredSpelling)

	t.Run("list_refuses_a_repository_that_names_no_repository", testReviewListRefusesARepositoryThatNamesNoRepository)

	t.Run("list_finds_a_repository_by_any_spelling_of_its_remote", testReviewListFindsARepositoryByAnySpellingOfItsRemote)

	t.Run("list_rejects_unknown_status_before_resolving_an_alias", testReviewListRejectsUnknownStatusBeforeResolvingAnAlias)

	t.Run("create_dry_run", testReviewCreateDryRun)

	t.Run("create_with_issue_dry_run", testReviewCreateWithIssueDryRun)

	t.Run("full_lifecycle_real_run", testReviewFullLifecycleRealRun)

	t.Run("create_name_conflict_real_run", testReviewCreateNameConflictRealRun)

	t.Run("comment_dry_run_traces_resolved_call", testReviewCommentDryRunTracesResolvedCall)

	t.Run("comment_reply_dry_run_traces_reply_to", testReviewCommentReplyDryRunTracesReplyTo)

	t.Run("resolve_dry_run_traces_resolved_call", testReviewResolveDryRunTracesResolvedCall)

	t.Run("unresolve_dry_run_traces_resolved_call", testReviewUnresolveDryRunTracesResolvedCall)

	t.Run("resolve_and_unresolve_root_real_run", testReviewResolveAndUnresolveRootRealRun)

	t.Run("resolve_reply_refused_names_root_real_run", testReviewResolveReplyRefusedNamesRootRealRun)

	t.Run("show_not_found_real_run", testReviewShowNotFoundRealRun)

	t.Run("record_build_dry_run_traces_resolved_call", testReviewRecordBuildDryRunTracesResolvedCall)

	t.Run("record_build_failed_dry_run_traces_failure_detail", testReviewRecordBuildFailedDryRunTracesFailureDetail)

	t.Run("record_build_real_run_moves_review_to_ready", testReviewRecordBuildRealRunMovesReviewToReady)

	t.Run("record_build_failed_real_run_moves_review_to_failed", testReviewRecordBuildFailedRealRunMovesReviewToFailed)

	t.Run("record_build_without_version_refused", testReviewRecordBuildWithoutVersionRefused)

	t.Run("record_build_failed_known_infrastructure_signature_refused", testReviewRecordBuildFailedKnownInfrastructureSignatureRefused)

	t.Run("report_merged_dry_run_traces_resolved_call", testReviewReportMergedDryRunTracesResolvedCall)

	t.Run("report_merged_real_run_moves_review_to_merged", testReviewReportMergedRealRunMovesReviewToMerged)

	t.Run("requeue_dry_run_traces_resolved_call", testReviewRequeueDryRunTracesResolvedCall)

	t.Run("requeue_real_run_moves_merge_back_to_ready", testReviewRequeueRealRunMovesMergeBackToReady)

	t.Run("requeue_refused_from_open", testReviewRequeueRefusedFromOpen)

	t.Run("requeue_refused_from_ready", testReviewRequeueRefusedFromReady)

	t.Run("requeue_refused_from_merged", testReviewRequeueRefusedFromMerged)

	t.Run("merge_queue_help", testReviewMergeQueueHelp)

	t.Run("merge_queue_list_dry_run", testReviewMergeQueueListDryRun)

	t.Run("merge_queue_is_the_repositorys_own", testReviewMergeQueueIsTheRepositorysOwn)

	t.Run("merge_queue_advance_empty_queue_real_run", testReviewMergeQueueAdvanceEmptyQueueRealRun)

	t.Run("merge_queue_advance_names_the_occupying_review", testReviewMergeQueueAdvanceNamesTheOccupyingReview)

	t.Run("merge_queue_override_advance_dry_run", testReviewMergeQueueOverrideAdvanceDryRun)

	t.Run("merge_queue_override_advance_requires_reason", testReviewMergeQueueOverrideAdvanceRequiresReason)

	t.Run("merge_queue_override_advance_empty_queue_real_run", testReviewMergeQueueOverrideAdvanceEmptyQueueRealRun)

	t.Run("output_json", testReviewOutputJSON)
}

func testReviewHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"review", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/help", normalize.Apply(result.Combined))
}

func testReviewListNoAliasConfigured(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"review", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no erun alias configured, got:\n%s", result.Combined)
	}
	golden.Equal(t, "review/list_no_alias_configured", normalize.Apply(result.Combined))
}

func testReviewListDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"review", "list", "--target-branch", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/list_dry_run", normalize.Apply(result.Combined))
}

func testReviewListMineDryRunTracesWhoamiAndPlaceholderFilter(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"review", "list", "--mine", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/list_mine_dry_run", normalize.Apply(result.Combined))
}

func testReviewListRejectsMineCombinedWithAuthorUserID(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"review", "list", "--mine", "--author-user-id", "user-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit combining --mine with --author-user-id, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "review/list_rejects_mine_combined_with_author_user_id", normalize.Apply(result.Combined))
}

// The reported failure: a mistyped --status reached the platform verbatim,
// came back as a clean empty listing, and printed "no reviews" at exit 0 --
// indistinguishable from a review queue that genuinely has nothing in that
// state. Both halves are asserted together below, because the defect is
// precisely that the two were the same output.
func testReviewListMistypedStatusIsRefusedRatherThanListedAsEmpty(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)

	// The control: a valid status that genuinely matches nothing. This one
	// legitimately reports an empty result at exit 0.
	empty := erun.Run(t, []string{"review", "list", "--status", "OPEN"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if empty.ExitCode != 0 {
		t.Fatalf("a valid status matching nothing should still exit 0, got %d:\n%s", empty.ExitCode, empty.Combined)
	}
	if !strings.Contains(empty.Combined, "no reviews") {
		t.Fatalf("expected the genuine empty listing to say 'no reviews', got:\n%s", empty.Combined)
	}

	// The reproduction: the same empty queue, reached by a typo.
	bogus := erun.Run(t, []string{"review", "list", "--status", "BOGUS"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if bogus.ExitCode == 0 {
		t.Fatalf("a mistyped --status must not exit 0, got:\n%s", bogus.Combined)
	}
	if strings.Contains(bogus.Combined, "no reviews") {
		t.Fatalf("a mistyped --status must not be reported as an empty listing, got:\n%s", bogus.Combined)
	}
	for _, want := range []string{"BOGUS", "OPEN", "CLOSED", "FAILED", "READY", "MERGE", "MERGED"} {
		if !strings.Contains(bogus.Combined, want) {
			t.Fatalf("expected the refusal to name %q, got:\n%s", want, bogus.Combined)
		}
	}
	golden.Equal(t, "review/list_mistyped_status_is_refused", normalize.Apply(bogus.Combined))

	// A near-miss typo is the same refusal: the defect was that any value
	// outside the vocabulary listed as empty, not only an obviously foreign one.
	nearMiss := erun.Run(t, []string{"review", "list", "--status", "MERGEDD"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if nearMiss.ExitCode == 0 || strings.Contains(nearMiss.Combined, "no reviews") {
		t.Fatalf("a near-miss --status must be refused too, got exit %d:\n%s", nearMiss.ExitCode, nearMiss.Combined)
	}
}

// Case-insensitivity is the documented behavior here, so a lower-case
// spelling must resolve to the stored one rather than be refused with it:
// the rejection is only for values outside the six in any casing.
func testReviewListLowercaseStatusResolvesToTheStoredSpelling(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

	result := erun.Run(t, []string{"review", "list", "--status", "open"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("a lower-case --status should resolve, not be refused, got %d:\n%s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "Add widget") {
		t.Fatalf("expected --status open to match the OPEN review, got:\n%s", result.Combined)
	}
}

// The reported failure: `--repository sophium/erun` names a repository on
// whichever forge the caller had in mind, so the platform answered it with
// the subset of rows a caller had once recorded under that same shorthand
// -- a silently short listing for a repository holding many reviews. It is
// refused as a bad argument, like a mistyped --status, and equally before
// the alias lookup: no alias is configured here on purpose.
func testReviewListRefusesARepositoryThatNamesNoRepository(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"review", "list", "--repository", "sophium/erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("a --repository that names no repository must not list, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "no reviews") {
		t.Fatalf("a --repository that names no repository must not be reported as an empty listing, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "git remote get-url origin") {
		t.Fatalf("expected the refusal to name the form to pass instead, not an alias-resolution failure, got:\n%s", result.Combined)
	}
}

// The other half: whichever spelling of the repository's own remote the
// caller holds reaches the rows the other spelling recorded. The stub
// matches the filter exactly, so a client that stopped canonicalizing
// would answer empty here.
func testReviewListFindsARepositoryByAnySpellingOfItsRemote(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

	for _, spelling := range []string{"git@github.com:sophium/erun.git", "https://github.com/sophium/erun"} {
		result := erun.Run(t, []string{"review", "list", "--repository", spelling}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("review list --repository %s exit %d:\n%s", spelling, result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "Add widget") {
			t.Fatalf("review list --repository %s did not find the review recorded under the same repository's other remote:\n%s", spelling, result.Combined)
		}
	}
}

// The refusal is a bad-argument error, so it must not depend on the platform
// being configured at all: a caller with no alias still learns their filter
// was wrong rather than being sent to set up an alias that would not help.
func testReviewListRejectsUnknownStatusBeforeResolvingAnAlias(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"review", "list", "--status", "BOGUS"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a mistyped --status, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "unsupported review status") {
		t.Fatalf("expected the refusal, not an alias-resolution failure, got:\n%s", result.Combined)
	}
}

func testReviewCreateDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"review", "create", "--name", "Add widget", "--repository", reviewRepository,
		"--source-branch", "feature/widget", "--target-branch", "main", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/create_dry_run", normalize.Apply(result.Combined))
}

// --issue is recorded, and recorded as declared: the trace has to name
// the reference, or a mistyped flag reads as a link the platform took.
func testReviewCreateWithIssueDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"review", "create", "--name", "Add widget", "--repository", reviewRepository,
		"--source-branch", "bug/2212-issue-ref-from-branch", "--target-branch", "main",
		"--issue", "sophium/erun#2683", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/create_with_issue_dry_run", normalize.Apply(result.Combined))
}

func testReviewFullLifecycleRealRun(t *testing.T) {
	// create -> list -> show -> comment -> reply -> close -> list again,
	// against the real stub server, covering every review/comment
	// PlatformClient method's request/response handling in one pass.
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)

	reviewID := createDeclaredIssueReview(t, setup)

	list := erun.Run(t, []string{"review", "list", "--source-branch", "feature/widget"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if list.ExitCode != 0 || !strings.Contains(list.Combined, "Add widget") {
		t.Fatalf("list exit %d: %s", list.ExitCode, list.Combined)
	}

	assertCommentShownOnReview(t, setup, reviewID)

	closed := erun.Run(t, []string{"review", "close", reviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if closed.ExitCode != 0 || !strings.Contains(closed.Combined, "status=CLOSED") {
		t.Fatalf("close exit %d: %s", closed.ExitCode, closed.Combined)
	}

	afterClose := erun.Run(t, []string{"review", "list", "--source-branch", "feature/widget", "--status", "OPEN"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if afterClose.ExitCode != 0 || !strings.Contains(afterClose.Combined, "no reviews") {
		t.Fatalf("expected no OPEN reviews after close, got:\n%s", afterClose.Combined)
	}
}

// createDeclaredIssueReview opens a review with a declared issue and requires
// the platform to answer with that issue as a DECLARED link.
func createDeclaredIssueReview(t *testing.T, setup env.Setup) string {
	t.Helper()
	create := erun.Run(t, []string{
		"review", "create", "--name", "Add widget", "--repository", reviewRepository,
		"--source-branch", "feature/widget", "--target-branch", "main",
		"--issue", "sophium/erun#2683", "--output", "json",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if create.ExitCode != 0 {
		t.Fatalf("create exit %d: %s", create.ExitCode, create.Combined)
	}
	var created struct {
		ReviewID       string `json:"reviewId"`
		IssueRef       string `json:"issueRef"`
		IssueRefSource string `json:"issueRefSource"`
	}
	if err := json.Unmarshal([]byte(create.Stdout), &created); err != nil {
		t.Fatalf("decode create --output json: %v\n%s", err, create.Stdout)
	}
	if created.ReviewID == "" {
		t.Fatalf("expected a non-empty reviewId, got:\n%s", create.Stdout)
	}
	// The declared issue travels the whole way: --issue into the request
	// body, and the platform's answer back out through the shared client
	// as a DECLARED link rather than one re-derived from the branch.
	if created.IssueRef != "sophium/erun#2683" || created.IssueRefSource != "DECLARED" {
		t.Fatalf("create --issue reported issueRef=%q issueRefSource=%q, want the declared link it recorded, got:\n%s",
			created.IssueRef, created.IssueRefSource, create.Stdout)
	}
	return created.ReviewID
}

// assertCommentShownOnReview posts one comment and requires show to render it
// alongside the review's declared issue.
func assertCommentShownOnReview(t *testing.T, setup env.Setup, reviewID string) {
	t.Helper()
	commentArgs := []string{"review", "comment", reviewID, "--commit", "abc123", "--file", "main.go", "--line", "42"}
	comment := erun.Run(t, commentArgs, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "nit: rename this\n"})
	if comment.ExitCode != 0 {
		t.Fatalf("comment exit %d: %s", comment.ExitCode, comment.Combined)
	}
	if !strings.Contains(comment.Combined, "main.go:42 nit: rename this") {
		t.Fatalf("expected the posted comment's file, line, and body in output, got:\n%s", comment.Combined)
	}

	show := erun.Run(t, []string{"review", "show", reviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if show.ExitCode != 0 || !strings.Contains(show.Combined, "nit: rename this") {
		t.Fatalf("show exit %d: %s", show.ExitCode, show.Combined)
	}
	if !strings.Contains(show.Combined, "issue=sophium/erun#2683 (declared)") {
		t.Fatalf("show did not render the review's declared issue, got:\n%s", show.Combined)
	}
	if !strings.Contains(show.Combined, "comments: 1") {
		t.Fatalf("expected show to report one comment, got:\n%s", show.Combined)
	}
}

func testReviewCreateNameConflictRealRun(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	args := []string{
		"review", "create", "--name", "dup", "--repository", reviewRepository,
		"--source-branch", "a", "--target-branch", "main",
	}
	first := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if first.ExitCode != 0 {
		t.Fatalf("first create exit %d: %s", first.ExitCode, first.Combined)
	}
	second := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if second.ExitCode == 0 {
		t.Fatalf("expected the second, colliding create to fail, got 0:\n%s", second.Combined)
	}
	if !strings.Contains(second.Combined, "conflict") {
		t.Fatalf("expected a conflict error, got:\n%s", second.Combined)
	}
}

func testReviewCommentDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"review", "comment", "review-1", "--commit", "abc123", "--file", "main.go", "--line", "42", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "nit: rename this\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/comment_dry_run", normalize.Apply(result.Combined))
}

func testReviewCommentReplyDryRunTracesReplyTo(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"review", "comment", "review-1", "--commit", "abc123", "--file", "main.go", "--line", "42", "--reply-to", "comment-1", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "good catch, fixed\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/comment_reply_dry_run", normalize.Apply(result.Combined))
}

func testReviewResolveDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"review", "resolve", "review-1", "comment-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/resolve_dry_run", normalize.Apply(result.Combined))
}

func testReviewUnresolveDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"review", "unresolve", "review-1", "comment-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/unresolve_dry_run", normalize.Apply(result.Combined))
}

func testReviewResolveAndUnresolveRootRealRun(t *testing.T) {
	// create -> comment (root) -> resolve -> show (unresolved: 0, status=CLOSED)
	// -> unresolve -> show (unresolved: 1, status=OPEN), covering
	// PlatformClient.UpdateCommentStatus's request/response handling and
	// ReviewDetail.UnresolvedThreads end to end.
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)

	created := createReviewJSON(t, setup, "Resolve test", "feature/resolve", "main")
	rootID := postCommentJSON(t, setup, created.ReviewID, "abc123", "main.go", 1, "root note", "")

	resolve := erun.Run(t, []string{"review", "resolve", created.ReviewID, rootID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if resolve.ExitCode != 0 || !strings.Contains(resolve.Combined, "status=CLOSED") {
		t.Fatalf("resolve exit %d: %s", resolve.ExitCode, resolve.Combined)
	}

	afterResolve := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if afterResolve.ExitCode != 0 || !strings.Contains(afterResolve.Combined, "unresolved threads: 0") {
		t.Fatalf("expected zero unresolved threads after resolve, got:\n%s", afterResolve.Combined)
	}
	if !strings.Contains(afterResolve.Combined, "status=CLOSED") {
		t.Fatalf("expected the root comment's status=CLOSED in show output, got:\n%s", afterResolve.Combined)
	}

	unresolve := erun.Run(t, []string{"review", "unresolve", created.ReviewID, rootID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if unresolve.ExitCode != 0 || !strings.Contains(unresolve.Combined, "status=OPEN") {
		t.Fatalf("unresolve exit %d: %s", unresolve.ExitCode, unresolve.Combined)
	}

	afterUnresolve := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if afterUnresolve.ExitCode != 0 || !strings.Contains(afterUnresolve.Combined, "unresolved threads: 1") {
		t.Fatalf("expected one unresolved thread after unresolve, got:\n%s", afterUnresolve.Combined)
	}
}

func testReviewResolveReplyRefusedNamesRootRealRun(t *testing.T) {
	// A status change addressed to a reply must be refused, naming the
	// thread's root comment id so the caller can retry against it.
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)

	created := createReviewJSON(t, setup, "Reply refusal test", "feature/reply-refusal", "main")
	rootID := postCommentJSON(t, setup, created.ReviewID, "abc123", "main.go", 1, "root note", "")
	replyID := postCommentJSON(t, setup, created.ReviewID, "abc123", "main.go", 1, "reply note", rootID)

	result := erun.Run(t, []string{"review", "resolve", created.ReviewID, replyID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected resolving a reply to fail, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, rootID) {
		t.Fatalf("expected the refusal to name root comment %s, got:\n%s", rootID, result.Combined)
	}
}

func testReviewShowNotFoundRealRun(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"review", "show", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing review, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "not found") {
		t.Fatalf("expected a not-found error, got:\n%s", result.Combined)
	}
}

func testReviewOutputJSON(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	args := []string{
		"review", "create", "--name", "json test", "--repository", reviewRepository,
		"--source-branch", "a", "--target-branch", "main", "--output", "json",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, `"name": "json test"`) {
		t.Fatalf("expected structured JSON result on stdout, got:\n%s", result.Combined)
	}
}
