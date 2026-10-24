package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testReviewRecordBuildDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"review", "record-build", "review-1",
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/record_build_dry_run", normalize.Apply(result.Combined))
}

func testReviewRecordBuildFailedDryRunTracesFailureDetail(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"review", "record-build", "review-1",
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
		"--failed", "--failure-detail", "image build failed", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/record_build_failed_dry_run", normalize.Apply(result.Combined))
}

func testReviewRecordBuildRealRunMovesReviewToReady(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

	args := []string{
		"review", "record-build", created.ReviewID,
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("record-build exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "successful=true") {
		t.Fatalf("expected the recorded build to report successful=true, got:\n%s", result.Combined)
	}

	show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=READY") {
		t.Fatalf("expected the review to be READY after a successful build, got exit %d:\n%s", show.ExitCode, show.Combined)
	}
}

func testReviewRecordBuildFailedRealRunMovesReviewToFailed(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

	args := []string{
		"review", "record-build", created.ReviewID,
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
		"--failed", "--failure-detail", "image build failed",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("record-build exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "successful=false") {
		t.Fatalf("expected the recorded build to report successful=false, got:\n%s", result.Combined)
	}

	show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=FAILED") {
		t.Fatalf("expected the review to be FAILED after a failed build, got exit %d:\n%s", show.ExitCode, show.Combined)
	}
}

func testReviewRecordBuildWithoutVersionRefused(t *testing.T) {
	// An omitted --version is refused before alias resolution or any platform call.
	setup := env.New(t)
	args := []string{
		"review", "record-build", "review-1",
		"--commit", "abc123def456abc123def456abc123def456abcd", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected the refusal to exit non-zero, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "review/record_build_without_version_refused", normalize.Apply(result.Combined))
}

func testReviewRecordBuildFailedKnownInfrastructureSignatureRefused(t *testing.T) {
	// A known infrastructure failure signature is refused locally rather than recorded FAILED:
	// builds have no INCONCLUSIVE result. No alias is seeded since the refusal comes first.
	setup := env.New(t)
	args := []string{
		"review", "record-build", "review-1",
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3", "--failed",
		"--failure-detail", "failed to solve: failed to resolve source metadata for ghcr.io/sophium/erun-devops:1.0.246: failed to authorize: failed to fetch oauth token: Post \"https://ghcr.io/token\": net/http: TLS handshake timeout",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected the refusal to exit non-zero, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "review/record_build_failed_known_infrastructure_signature_refused", normalize.Apply(result.Combined))
}

func testReviewReportMergedDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{
		"review", "report-merged", "review-1",
		"--build-id", "build-1", "--remote-url", "https://github.com/org/repo.git", "--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/report_merged_dry_run", normalize.Apply(result.Combined))
}

func testReviewReportMergedRealRunMovesReviewToMerged(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

	args := []string{
		"review", "report-merged", created.ReviewID,
		"--build-id", "build-1", "--remote-url", "https://github.com/org/repo.git",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("report-merged exit %d: %s", result.ExitCode, result.Combined)
	}

	show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=MERGED") {
		t.Fatalf("expected the review to be MERGED after report-merged, got exit %d:\n%s", show.ExitCode, show.Combined)
	}
}

func testReviewRequeueDryRunTracesResolvedCall(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"review", "requeue", "review-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "review/requeue_dry_run", normalize.Apply(result.Combined))
}

func testReviewRequeueRealRunMovesMergeBackToReady(t *testing.T) {
	// requeue frees a review wedged at MERGE without a raw HTTP call.
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

	build := erun.Run(t, []string{
		"review", "record-build", created.ReviewID,
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if build.ExitCode != 0 {
		t.Fatalf("record-build exit %d: %s", build.ExitCode, build.Combined)
	}

	advance := erun.Run(t, []string{"review", "queue", "advance", "--target-branch", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if advance.ExitCode != 0 || !strings.Contains(advance.Combined, "status=MERGE") {
		t.Fatalf("advance exit %d: %s", advance.ExitCode, advance.Combined)
	}

	requeue := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if requeue.ExitCode != 0 || !strings.Contains(requeue.Combined, "status=READY") {
		t.Fatalf("requeue exit %d: %s", requeue.ExitCode, requeue.Combined)
	}

	show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=READY") {
		t.Fatalf("expected the review to be READY after requeue, got exit %d:\n%s", show.ExitCode, show.Combined)
	}
}

func testReviewRequeueRefusedFromOpen(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	created := createReviewJSON(t, setup, "Add widget", "feature/widget-open", "main")

	result := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit requeuing an OPEN review, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "OPEN") || !strings.Contains(result.Combined, "not MERGE") {
		t.Fatalf("expected the refusal to name the review's actual status OPEN, got:\n%s", result.Combined)
	}
}

func testReviewRequeueRefusedFromReady(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	created := createReviewJSON(t, setup, "Add widget", "feature/widget-ready", "main")
	build := erun.Run(t, []string{
		"review", "record-build", created.ReviewID,
		"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if build.ExitCode != 0 {
		t.Fatalf("record-build exit %d: %s", build.ExitCode, build.Combined)
	}

	result := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit requeuing a READY review, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "READY") || !strings.Contains(result.Combined, "not MERGE") {
		t.Fatalf("expected the refusal to name the review's actual status READY, got:\n%s", result.Combined)
	}
}

func testReviewRequeueRefusedFromMerged(t *testing.T) {
	setup := env.New(t)
	server := reviewAPIStubServer(t)
	platformAlias(t, setup, server)
	created := createReviewJSON(t, setup, "Add widget", "feature/widget-merged", "main")

	reportMerged := erun.Run(t, []string{
		"review", "report-merged", created.ReviewID,
		"--build-id", "build-1", "--remote-url", "https://github.com/org/repo.git",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if reportMerged.ExitCode != 0 {
		t.Fatalf("report-merged exit %d: %s", reportMerged.ExitCode, reportMerged.Combined)
	}

	result := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit requeuing a MERGED review, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "MERGED") || !strings.Contains(result.Combined, "not MERGE") {
		t.Fatalf("expected the refusal to name the review's actual status MERGED, got:\n%s", result.Combined)
	}
}
