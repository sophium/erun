package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Review authorship, reviewer assignment, and the one-live-review-per-branch
// index all live in SQL (a DB-side default, a join table under RLS, a partial
// unique index), so they are exercised against a real migrated PostgreSQL
// rather than a fake that agrees with itself.
func reviewsDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_REVIEWS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_REVIEWS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantID := seedReviewsTenant(t, db, "reviews-e2e")
	t.Cleanup(func() { clearReviewsTenant(t, db, tenantID) })
	return db, tenantID
}

func seedReviewsTenant(t *testing.T, db *sql.DB, label string) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		label+"-"+time.Now().Format("20060102150405.000000"),
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	return tenantID
}

func clearReviewsTenant(t *testing.T, db *sql.DB, tenantID string) {
	t.Helper()
	// reviews and builds reference each other (last_*_build_id / review_id), so
	// the cycle has to be broken before either can be deleted.
	if _, err := db.Exec(`
		UPDATE reviews
		   SET status = 'CLOSED', last_failed_build_id = NULL, last_ready_build_id = NULL, last_merged_build_id = NULL
		 WHERE tenant_id = $1
	`, tenantID); err != nil {
		t.Logf("unlinking reviews from builds for tenant %s: %v", tenantID, err)
	}
	for _, table := range []string{"review_reviewers", "review_merge_queue", "builds", "reviews", "users", "tenants"} {
		if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
		}
	}
}

func seedReviewsUser(t *testing.T, db *sql.DB, tenantID, username string) string {
	t.Helper()
	var userID string
	err := db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, username,
	).Scan(&userID)
	mustNoErr(t, err, "seed user "+username)
	return userID
}

func reviewsContext(tenantID, userID string) context.Context {
	return security.WithContext(context.Background(), security.Context{
		TenantID: tenantID, TenantType: "COMPANY", ErunUserID: userID,
	})
}

// TestReviewAuthorDefaultsToTheAuthenticatedCallerAndIgnoresACallerSuppliedOne
// is the impersonation guard the issue calls for: erun_current_user_id() —
// not a client-asserted field — decides who a review's author is.
func TestReviewAuthorDefaultsToTheAuthenticatedCallerAndIgnoresACallerSuppliedOne(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	impersonated := seedReviewsUser(t, db, tenantID, "impersonated")
	repo := NewReviewRepository(NewTxManager(db, DialectPostgres))
	ctx := reviewsContext(tenantID, author)

	created, err := repo.Create(ctx, model.Review{
		AuthorUserID: impersonated,
		Name:         "authored review",
		TargetBranch: "main",
		SourceBranch: "feature/author-default",
		Status:       model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create review")
	if created.AuthorUserID != author {
		t.Fatalf("author = %q, want the authenticated caller %q (a caller-supplied author must be ignored)", created.AuthorUserID, author)
	}

	fetched, err := repo.Get(ctx, created.ReviewID)
	mustNoErr(t, err, "get review")
	if fetched.AuthorUserID != author {
		t.Fatalf("stored author = %q, want %q", fetched.AuthorUserID, author)
	}
}

// TestReviewReviewersCanBeAddedListedAndRemoved proves point 2 of the issue:
// a review can be directed at more than one person, and removing one leaves
// the others.
func TestReviewReviewersCanBeAddedListedAndRemoved(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	first := seedReviewsUser(t, db, tenantID, "reviewer-1")
	second := seedReviewsUser(t, db, tenantID, "reviewer-2")
	third := seedReviewsUser(t, db, tenantID, "reviewer-3")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	reviewers := NewReviewReviewerRepository(NewTxManager(db, DialectPostgres))

	review, err := reviews.Create(ctx, model.Review{
		Name: "reviewed review", TargetBranch: "main", SourceBranch: "feature/reviewers", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create review")

	for _, reviewer := range []string{first, second, third} {
		_, err := reviewers.Create(ctx, model.ReviewReviewer{ReviewID: review.ReviewID, UserID: reviewer})
		mustNoErr(t, err, "add reviewer "+reviewer)
	}

	listed, err := reviewers.List(ctx, ReviewReviewerFilter{ReviewID: review.ReviewID})
	mustNoErr(t, err, "list reviewers")
	if len(listed) != 3 {
		t.Fatalf("listed %d reviewers, want 3", len(listed))
	}

	mustNoErr(t, reviewers.Delete(ctx, review.ReviewID, second), "remove reviewer")

	listed, err = reviewers.List(ctx, ReviewReviewerFilter{ReviewID: review.ReviewID})
	mustNoErr(t, err, "list reviewers after removal")
	if len(listed) != 2 {
		t.Fatalf("listed %d reviewers after removal, want 2", len(listed))
	}
	for _, reviewer := range listed {
		if reviewer.UserID == second {
			t.Fatalf("removed reviewer %s is still listed", second)
		}
	}

	if err := reviewers.Delete(ctx, review.ReviewID, second); err == nil {
		t.Fatal("removing an already-removed reviewer succeeded, want ErrNotFound")
	}
}

// TestReviewReviewerFromAnotherTenantIsRefusedByTheFK is the validation the
// issue names explicitly: a reviewer must belong to the same tenant as the
// review.
func TestReviewReviewerFromAnotherTenantIsRefusedByTheFK(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	reviewers := NewReviewReviewerRepository(NewTxManager(db, DialectPostgres))

	review, err := reviews.Create(ctx, model.Review{
		Name: "cross tenant reviewer", TargetBranch: "main", SourceBranch: "feature/cross-tenant", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create review")

	otherTenantID := seedReviewsTenant(t, db, "reviews-e2e-other")
	t.Cleanup(func() { clearReviewsTenant(t, db, otherTenantID) })
	outsider := seedReviewsUser(t, db, otherTenantID, "outsider")

	_, err = reviewers.Create(ctx, model.ReviewReviewer{ReviewID: review.ReviewID, UserID: outsider})
	if err == nil {
		t.Fatal("a reviewer from another tenant was accepted, want the FK to refuse it")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want it to unwrap to ErrNotFound (so the API reports 404, not a bare 500)", err)
	}
}

// TestReviewDiscoveryFiltersAnswerMyReviewsAndReviewsWaitingOnMe is the
// discovery gap the issue opens with: today only targetBranch is filterable.
func TestReviewDiscoveryFiltersAnswerMyReviewsAndReviewsWaitingOnMe(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	me := seedReviewsUser(t, db, tenantID, "me")
	someoneElse := seedReviewsUser(t, db, tenantID, "someone-else")
	ctxMe := reviewsContext(tenantID, me)
	ctxSomeoneElse := reviewsContext(tenantID, someoneElse)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	reviewers := NewReviewReviewerRepository(NewTxManager(db, DialectPostgres))

	mine, err := reviews.Create(ctxMe, model.Review{
		Name: "my open review", TargetBranch: "main", SourceBranch: "feature/mine", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create my review")
	theirs, err := reviews.Create(ctxSomeoneElse, model.Review{
		Name: "their review awaiting me", TargetBranch: "main", SourceBranch: "feature/theirs", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create their review")
	_, err = reviewers.Create(ctxSomeoneElse, model.ReviewReviewer{ReviewID: theirs.ReviewID, UserID: me})
	mustNoErr(t, err, "assign me as a reviewer")

	// "my reviews": authored by me, open.
	myReviews, err := reviews.List(ctxMe, ReviewFilter{AuthorUserID: me, Status: model.ReviewStatusOpen})
	mustNoErr(t, err, "list my reviews")
	if len(myReviews) != 1 || myReviews[0].ReviewID != mine.ReviewID {
		t.Fatalf("authorUserId+status filter returned %+v, want exactly %s", myReviews, mine.ReviewID)
	}

	// "reviews waiting on me": I am a reviewer.
	waitingOnMe, err := reviews.List(ctxMe, ReviewFilter{ReviewerUserID: me})
	mustNoErr(t, err, "list reviews waiting on me")
	if len(waitingOnMe) != 1 || waitingOnMe[0].ReviewID != theirs.ReviewID {
		t.Fatalf("reviewerUserId filter returned %+v, want exactly %s", waitingOnMe, theirs.ReviewID)
	}

	// sourceBranch composes with the existing targetBranch filter.
	bySourceBranch, err := reviews.List(ctxMe, ReviewFilter{TargetBranch: "main", SourceBranch: "feature/mine"})
	mustNoErr(t, err, "list by source+target branch")
	if len(bySourceBranch) != 1 || bySourceBranch[0].ReviewID != mine.ReviewID {
		t.Fatalf("sourceBranch+targetBranch filter returned %+v, want exactly %s", bySourceBranch, mine.ReviewID)
	}
}

// TestOnlyOneLiveReviewPerSourceAndTargetBranch proves point 4: the second
// review is refused while the first is live, and accepted once the first is
// MERGED or CLOSED, because branch history must stay unbounded.
func TestOnlyOneLiveReviewPerSourceAndTargetBranch(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	first, err := reviews.Create(ctx, model.Review{
		Name: "first proposal", TargetBranch: "main", SourceBranch: "feature/duplicate", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create first review")

	if _, err := reviews.Create(ctx, model.Review{
		Name: "second proposal", TargetBranch: "main", SourceBranch: "feature/duplicate", Status: model.ReviewStatusOpen,
	}); err == nil {
		t.Fatal("a second live review on the same source/target branch was accepted")
	} else if !errors.Is(err, ErrConflict) {
		t.Fatalf("second review error = %v, want ErrConflict", err)
	}

	// Closing the first frees the branch pair up for a fresh proposal.
	first.Status = model.ReviewStatusClosed
	closed, err := reviews.Update(ctx, first)
	mustNoErr(t, err, "close first review")
	if closed.Status != model.ReviewStatusClosed {
		t.Fatalf("status = %q, want CLOSED", closed.Status)
	}
	afterClose, err := reviews.Create(ctx, model.Review{
		Name: "third proposal", TargetBranch: "main", SourceBranch: "feature/duplicate", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create review after the first closed")

	// A second live review is refused again; merging (rather than closing)
	// the live one frees the pair the same way.
	if _, err := reviews.Create(ctx, model.Review{
		Name: "fourth proposal", TargetBranch: "main", SourceBranch: "feature/duplicate", Status: model.ReviewStatusOpen,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("review while afterClose is live error = %v, want ErrConflict", err)
	}
	build, err := builds.Create(ctx, model.Build{ReviewID: afterClose.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "commit-merge", Version: "1.0.0"})
	mustNoErr(t, err, "create merge build")
	afterClose.Status = model.ReviewStatusMerged
	afterClose.LastMergedBuildID = build.BuildID
	merged, err := reviews.Update(ctx, afterClose)
	mustNoErr(t, err, "merge review")
	if merged.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %q, want MERGED", merged.Status)
	}
	if _, err := reviews.Create(ctx, model.Review{
		Name: "fifth proposal", TargetBranch: "main", SourceBranch: "feature/duplicate", Status: model.ReviewStatusOpen,
	}); err != nil {
		t.Fatalf("review after the live one merged: %v, want it accepted", err)
	}
}

// TestReviewTenantIsolation proves a caller from one tenant cannot see or
// touch another tenant's reviews, the same RLS boundary every tenant-owned
// table must hold.
func TestReviewTenantIsolation(t *testing.T) {
	db, tenantA := reviewsDatabase(t)
	authorA := seedReviewsUser(t, db, tenantA, "tenant-a-author")
	ctxA := reviewsContext(tenantA, authorA)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))

	reviewA, err := reviews.Create(ctxA, model.Review{
		Name: "tenant a review", TargetBranch: "main", SourceBranch: "feature/tenant-a", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create tenant A review")

	tenantB := seedReviewsTenant(t, db, "reviews-e2e-tenant-b")
	t.Cleanup(func() { clearReviewsTenant(t, db, tenantB) })
	authorB := seedReviewsUser(t, db, tenantB, "tenant-b-author")
	ctxB := reviewsContext(tenantB, authorB)

	_, err = reviews.Create(ctxB, model.Review{
		Name: "tenant b review", TargetBranch: "main", SourceBranch: "feature/tenant-b", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create tenant B review")

	listedByB, err := reviews.List(ctxB, ReviewFilter{})
	mustNoErr(t, err, "list as tenant B")
	for _, review := range listedByB {
		if review.ReviewID == reviewA.ReviewID {
			t.Fatalf("tenant B's list included tenant A's review %s", reviewA.ReviewID)
		}
	}

	if _, err := reviews.Get(ctxB, reviewA.ReviewID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant B fetching tenant A's review by ID: err = %v, want ErrNotFound", err)
	}
}

// openReview is a small helper for the builds.kind tests below: they only
// need a review to attach a build to, not any particular review workflow.
func openReview(t *testing.T, reviews *ReviewRepository, ctx context.Context, name, sourceBranch string) model.Review {
	t.Helper()
	review, err := reviews.Create(ctx, model.Review{
		Name: name, TargetBranch: "main", SourceBranch: sourceBranch, Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create review "+name)
	return review
}

// TestGateBuildContractAllowsNoVersionAndRequiresFailureDetail is the
// database contract #1196 needs `builds.kind` for: a GATE build (the merge
// queue's own prospective-merge build) mints no version, so it must be
// insertable without one; a RECORDED build (a client-reported build, or a
// release's own) still requires one; and a failed GATE build must carry
// failure_detail in the gate's own words.
func TestGateBuildContractAllowsNoVersionAndRequiresFailureDetail(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	review := openReview(t, reviews, ctx, "gate build contract review", "feature/gate-contract")

	successful, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindGate, Successful: true, CommitID: "merge-sha-ok",
	})
	mustNoErr(t, err, "create a successful GATE build with no version")
	if successful.Version != "" {
		t.Fatalf("version = %q, want empty for a GATE build", successful.Version)
	}

	if _, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindGate, Successful: false, CommitID: "merge-sha-bad",
	}); err == nil {
		t.Fatal("a failed GATE build with no failure_detail was accepted, want the CHECK constraint to refuse it")
	}

	failed, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindGate, Successful: false, CommitID: "merge-sha-bad",
		FailureDetail: "erun build failed: go vet found 3 issues",
	})
	mustNoErr(t, err, "create a failed GATE build with failure_detail")
	if failed.FailureDetail == "" {
		t.Fatal("failureDetail was not persisted for a failed GATE build")
	}

	if _, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "source-sha",
	}); err == nil {
		t.Fatal("a RECORDED build with no version was accepted, want the CHECK constraint to require one")
	}

	recorded, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "source-sha", Version: "1.0.0",
	})
	mustNoErr(t, err, "create a RECORDED build with a version")
	if recorded.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", recorded.Version)
	}
}

// TestBuildTenantIsolation proves a caller from one tenant cannot see or fetch
// another tenant's builds, including a GATE build the merge queue produced —
// the same RLS boundary every tenant-owned table must hold.
func TestBuildTenantIsolation(t *testing.T) {
	db, tenantA := reviewsDatabase(t)
	authorA := seedReviewsUser(t, db, tenantA, "tenant-a-author")
	ctxA := reviewsContext(tenantA, authorA)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	reviewA := openReview(t, reviews, ctxA, "tenant a build review", "feature/tenant-a-build")
	buildA, err := builds.Create(ctxA, model.Build{
		ReviewID: reviewA.ReviewID, Kind: model.BuildKindGate, Successful: true, CommitID: "merge-sha-tenant-a",
	})
	mustNoErr(t, err, "create tenant A's build")

	tenantB := seedReviewsTenant(t, db, "reviews-e2e-tenant-b-builds")
	t.Cleanup(func() { clearReviewsTenant(t, db, tenantB) })
	authorB := seedReviewsUser(t, db, tenantB, "tenant-b-author")
	ctxB := reviewsContext(tenantB, authorB)

	reviewB := openReview(t, reviews, ctxB, "tenant b build review", "feature/tenant-b-build")
	_, err = builds.Create(ctxB, model.Build{
		ReviewID: reviewB.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "commit-b", Version: "1.0.0",
	})
	mustNoErr(t, err, "create tenant B's build")

	listedByB, err := builds.List(ctxB, BuildFilter{})
	mustNoErr(t, err, "list builds as tenant B")
	for _, build := range listedByB {
		if build.BuildID == buildA.BuildID {
			t.Fatalf("tenant B's build list included tenant A's build %s", buildA.BuildID)
		}
	}

	if _, err := builds.Get(ctxB, buildA.BuildID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant B fetching tenant A's build by ID: err = %v, want ErrNotFound", err)
	}
}
