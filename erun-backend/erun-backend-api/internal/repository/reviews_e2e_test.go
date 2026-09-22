package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"

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
	return seedReviewsTenantOfType(t, db, label, "COMPANY")
}

// seedReviewsTenantOfType is seedReviewsTenant's general form, for the
// OPERATIONS-caller scoping regression tests below: erun_operations' RLS
// policy is unconditional (USING (true)), so only an OPERATIONS-typed tenant
// can actually exercise the bypass List/ListMergeQueue must guard against.
func seedReviewsTenantOfType(t *testing.T, db *sql.DB, label string, tenantType string) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING tenant_id`,
		label+"-"+time.Now().Format("20060102150405.000000"), tenantType,
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

// reviewsContextOfType is reviewsContext's general form, for the
// OPERATIONS-caller scoping regression tests below.
func reviewsContextOfType(tenantID, userID, tenantType string) context.Context {
	return security.WithContext(context.Background(), security.Context{
		TenantID: tenantID, TenantType: tenantType, ErunUserID: userID,
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

	if _, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindGate, Successful: true, CommitID: "merge-sha-versioned", Version: "1.0.0",
	}); err == nil {
		t.Fatal("a GATE build with a version was accepted, want the CHECK constraint to refuse it: the gate publishes nothing and mints no version")
	}
}

// TestBuildProfileRoundTripsThroughCreateAndGet proves the bounded per-build
// profile survives a real jsonb column round trip -- Get uses a hand-written
// SELECT column list, unlike Bun's usual
// Model(&x).Scan, so a column added to the table without a matching addition
// to that SELECT would silently read back as the field's zero value rather
// than failing loudly.
func TestBuildProfileRoundTripsThroughCreateAndGet(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	review := openReview(t, reviews, ctx, "build profile review", "feature/build-profile")

	profile := &eruncommon.BuildProfileSummary{
		DurationSeconds: 42.5,
		TotalStepCount:  2,
		TopSteps: []eruncommon.BuildProfileStepSummary{
			{Name: "erun-devops", DurationSeconds: 40},
			{Name: "erun-devops > linux/amd64", DurationSeconds: 39},
		},
	}
	created, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true,
		CommitID: "profile-sha", Version: "1.0.0", Profile: profile,
	})
	mustNoErr(t, err, "create a RECORDED build with a profile")
	if created.Profile == nil {
		t.Fatal("Create did not return the profile it was given")
	}

	fetched, err := builds.Get(ctx, created.BuildID)
	mustNoErr(t, err, "get the build back")
	if fetched.Profile == nil {
		t.Fatal("Get did not read back a profile")
	}
	if fetched.Profile.DurationSeconds != 42.5 || fetched.Profile.TotalStepCount != 2 {
		t.Fatalf("profile totals did not round-trip: got %+v", fetched.Profile)
	}
	if len(fetched.Profile.TopSteps) != 2 || fetched.Profile.TopSteps[0].Name != "erun-devops" {
		t.Fatalf("profile top steps did not round-trip: got %+v", fetched.Profile.TopSteps)
	}

	noProfile, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true,
		CommitID: "no-profile-sha", Version: "1.0.1",
	})
	mustNoErr(t, err, "create a RECORDED build with no profile")
	if noProfile.Profile != nil {
		t.Fatalf("expected a nil profile for a build that reported none, got %+v", noProfile.Profile)
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

// TestReviewListScopesToTheOperationsCallersOwnTenant pins the failure
// scenario directly: an OPERATIONS caller's List must not include a stranger
// tenant's reviews even though erun_operations' RLS policy makes them visible
// too.
func TestReviewListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	db, strangerTenantID := reviewsDatabase(t)
	strangerAuthor := seedReviewsUser(t, db, strangerTenantID, "stranger-author")
	strangerCtx := reviewsContext(strangerTenantID, strangerAuthor)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	_ = openReview(t, reviews, strangerCtx, "stranger review", "feature/stranger")

	opsTenantID := seedReviewsTenantOfType(t, db, "reviews-e2e-ops", "OPERATIONS")
	t.Cleanup(func() { clearReviewsTenant(t, db, opsTenantID) })
	opsAuthor := seedReviewsUser(t, db, opsTenantID, "ops-author")
	opsCtx := reviewsContextOfType(opsTenantID, opsAuthor, "OPERATIONS")
	own := openReview(t, reviews, opsCtx, "ops review", "feature/ops")

	listed, err := reviews.List(opsCtx, ReviewFilter{})
	mustNoErr(t, err, "list as operations caller")
	if len(listed) != 1 || listed[0].ReviewID != own.ReviewID {
		t.Fatalf("List = %+v, want exactly the operations caller's own review %s, not the stranger's as well", listed, own.ReviewID)
	}
}

// TestReviewListMergeQueueScopesToTheOperationsCallersOwnTenant pins the same
// failure scenario for the merge queue: an OPERATIONS caller's
// ListMergeQueue must not include a stranger tenant's queued review.
func TestReviewListMergeQueueScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	db, strangerTenantID := reviewsDatabase(t)
	strangerAuthor := seedReviewsUser(t, db, strangerTenantID, "stranger-author")
	strangerCtx := reviewsContext(strangerTenantID, strangerAuthor)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))
	strangerReview := openReview(t, reviews, strangerCtx, "stranger queued review", "feature/stranger-queue")
	strangerBuild, err := builds.Create(strangerCtx, model.Build{
		ReviewID: strangerReview.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "stranger-sha", Version: "1.0.0",
	})
	mustNoErr(t, err, "create stranger build")
	strangerReview.Status = model.ReviewStatusReady
	strangerReview.LastReadyBuildID = strangerBuild.BuildID
	strangerReview, err = reviews.Update(strangerCtx, strangerReview)
	mustNoErr(t, err, "mark stranger review READY")
	_, err = reviews.CreateMergeQueueEntry(strangerCtx, model.ReviewMergeQueueEntry{
		TargetBranch: strangerReview.TargetBranch, ReviewID: strangerReview.ReviewID,
	})
	mustNoErr(t, err, "queue stranger review")

	opsTenantID := seedReviewsTenantOfType(t, db, "reviews-e2e-ops-queue", "OPERATIONS")
	t.Cleanup(func() { clearReviewsTenant(t, db, opsTenantID) })
	opsAuthor := seedReviewsUser(t, db, opsTenantID, "ops-author")
	opsCtx := reviewsContextOfType(opsTenantID, opsAuthor, "OPERATIONS")
	opsReview := openReview(t, reviews, opsCtx, "ops queued review", "feature/ops-queue")
	opsBuild, err := builds.Create(opsCtx, model.Build{
		ReviewID: opsReview.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "ops-sha", Version: "1.0.0",
	})
	mustNoErr(t, err, "create ops build")
	opsReview.Status = model.ReviewStatusReady
	opsReview.LastReadyBuildID = opsBuild.BuildID
	opsReview, err = reviews.Update(opsCtx, opsReview)
	mustNoErr(t, err, "mark ops review READY")
	_, err = reviews.CreateMergeQueueEntry(opsCtx, model.ReviewMergeQueueEntry{
		TargetBranch: opsReview.TargetBranch, ReviewID: opsReview.ReviewID,
	})
	mustNoErr(t, err, "queue ops review")

	listed, err := reviews.ListMergeQueue(opsCtx, "", "")
	mustNoErr(t, err, "list merge queue as operations caller")
	if len(listed) != 1 || listed[0].ReviewID != opsReview.ReviewID {
		t.Fatalf("ListMergeQueue = %+v, want exactly the operations caller's own queued review %s, not the stranger's as well", listed, opsReview.ReviewID)
	}
}

// TestBuildListScopesToTheOperationsCallersOwnTenant pins the failure
// scenario for builds: an OPERATIONS caller's List must not include a
// stranger tenant's builds even though erun_operations' RLS policy makes
// them visible too.
func TestBuildListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	db, strangerTenantID := reviewsDatabase(t)
	strangerAuthor := seedReviewsUser(t, db, strangerTenantID, "stranger-author")
	strangerCtx := reviewsContext(strangerTenantID, strangerAuthor)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))
	strangerReview := openReview(t, reviews, strangerCtx, "stranger build review", "feature/stranger-build")
	_, err := builds.Create(strangerCtx, model.Build{
		ReviewID: strangerReview.ReviewID, Kind: model.BuildKindGate, Successful: true, CommitID: "merge-sha-stranger",
	})
	mustNoErr(t, err, "create stranger build")

	opsTenantID := seedReviewsTenantOfType(t, db, "reviews-e2e-ops-build", "OPERATIONS")
	t.Cleanup(func() { clearReviewsTenant(t, db, opsTenantID) })
	opsAuthor := seedReviewsUser(t, db, opsTenantID, "ops-author")
	opsCtx := reviewsContextOfType(opsTenantID, opsAuthor, "OPERATIONS")
	opsReview := openReview(t, reviews, opsCtx, "ops build review", "feature/ops-build")
	opsBuild, err := builds.Create(opsCtx, model.Build{
		ReviewID: opsReview.ReviewID, Kind: model.BuildKindGate, Successful: true, CommitID: "merge-sha-ops",
	})
	mustNoErr(t, err, "create ops build")

	listed, err := builds.List(opsCtx, BuildFilter{})
	mustNoErr(t, err, "list builds as operations caller")
	if len(listed) != 1 || listed[0].BuildID != opsBuild.BuildID {
		t.Fatalf("List = %+v, want exactly the operations caller's own build %s, not the stranger's as well", listed, opsBuild.BuildID)
	}
}

// TestReviewReviewerListScopesToTheOperationsCallersOwnTenant pins the
// failure scenario for review reviewers: an OPERATIONS caller's List must
// not include a stranger tenant's reviewer assignments even though
// erun_operations' RLS policy makes them visible too.
func TestReviewReviewerListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	db, strangerTenantID := reviewsDatabase(t)
	strangerAuthor := seedReviewsUser(t, db, strangerTenantID, "stranger-author")
	strangerReviewer := seedReviewsUser(t, db, strangerTenantID, "stranger-reviewer")
	strangerCtx := reviewsContext(strangerTenantID, strangerAuthor)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	reviewers := NewReviewReviewerRepository(NewTxManager(db, DialectPostgres))
	strangerReview := openReview(t, reviews, strangerCtx, "stranger reviewed review", "feature/stranger-reviewers")
	_, err := reviewers.Create(strangerCtx, model.ReviewReviewer{ReviewID: strangerReview.ReviewID, UserID: strangerReviewer})
	mustNoErr(t, err, "add stranger reviewer")

	opsTenantID := seedReviewsTenantOfType(t, db, "reviews-e2e-ops-reviewers", "OPERATIONS")
	t.Cleanup(func() { clearReviewsTenant(t, db, opsTenantID) })
	opsAuthor := seedReviewsUser(t, db, opsTenantID, "ops-author")
	opsReviewer := seedReviewsUser(t, db, opsTenantID, "ops-reviewer")
	opsCtx := reviewsContextOfType(opsTenantID, opsAuthor, "OPERATIONS")
	opsReview := openReview(t, reviews, opsCtx, "ops reviewed review", "feature/ops-reviewers")
	_, err = reviewers.Create(opsCtx, model.ReviewReviewer{ReviewID: opsReview.ReviewID, UserID: opsReviewer})
	mustNoErr(t, err, "add ops reviewer")

	listed, err := reviewers.List(opsCtx, ReviewReviewerFilter{})
	mustNoErr(t, err, "list reviewers as operations caller")
	if len(listed) != 1 || listed[0].UserID != opsReviewer {
		t.Fatalf("List = %+v, want exactly the operations caller's own reviewer %s, not the stranger's as well", listed, opsReviewer)
	}
}

// TestReviewRepositoryScopesNameUniquenessToTheRepository is the reported
// failure against real Postgres: two repositories a tenant serves have
// branches with the same names and, in a rebased-again workflow, the same
// squash-merge message, so a name reserved tenant-wide made the second
// repository's review unopenable for a reason that had nothing to do with it.
func TestReviewRepositoryScopesNameUniquenessToTheRepository(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))

	first, err := reviews.Create(ctx, model.Review{
		Repository: "https://github.com/sophium/erun", Name: "Fix the widget",
		TargetBranch: "main", SourceBranch: "feature/widget", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create the first repository's review")

	second, err := reviews.Create(ctx, model.Review{
		Repository: "https://github.com/sophium/other", Name: "Fix the widget",
		TargetBranch: "main", SourceBranch: "feature/widget", Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create the same message and branch pair in a second repository")

	if second.Name != first.Name || second.Repository == first.Repository {
		t.Fatalf("second review = %+v, want the same name in a different repository", second)
	}

	if _, err := reviews.Create(ctx, model.Review{
		Repository: "https://github.com/sophium/erun", Name: "Fix the widget",
		TargetBranch: "main", SourceBranch: "feature/other", Status: model.ReviewStatusOpen,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second review with the same name in the same repository: err = %v, want ErrConflict", err)
	}
}

// TestReviewClosedReviewReleasesItsName is the reported failure against real
// Postgres: a review's name is the squash-merge message it will land under, so
// names are unique per repository only among reviews that can still reach
// MERGED or did reach it. A CLOSED review never landed, so its name was never
// used as a merge message and must reserve nothing — re-reviewing a rebased
// branch under the subject its own commit already carries is an ordinary
// workflow, and refusing it forced a reworded message for no reason a reader
// could see.
//
// Repository, source branch, and target branch are all held constant across the
// two creates on purpose: that is the reported reproduction (`erun review
// close`, then `erun review create` for the same branch with the same name), and
// the one-live-review-per-branch index has to release the pair for exactly the
// same reason the name index has to release the name. The first create is kept
// as an in-test control — it proves the name really was taken while the review
// was live, so the create after the close is exercising the release rather than
// a name that was never reserved.
func TestReviewClosedReviewReleasesItsName(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))

	const repository = "https://github.com/sophium/erun"
	const message = "Fix step-timing canonicalization silently disabling on a failed row"
	const branch = "bug/2076-auth-retry-gate-venue"

	abandoned, err := reviews.Create(ctx, model.Review{
		Repository: repository, Name: message,
		TargetBranch: "main", SourceBranch: branch, Status: model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create the review that is about to be closed")

	create := func() (model.Review, error) {
		return reviews.Create(ctx, model.Review{
			Repository: repository, Name: message,
			TargetBranch: "main", SourceBranch: branch, Status: model.ReviewStatusOpen,
		})
	}

	if _, err := create(); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-using a live review's name: err = %v, want ErrConflict", err)
	}

	abandoned.Status = model.ReviewStatusClosed
	if _, err := reviews.Update(ctx, abandoned); err != nil {
		t.Fatalf("close the review: %v", err)
	}

	reopened, err := create()
	mustNoErr(t, err, "re-review the branch under the name its closed review held")

	if reopened.Name != message || reopened.Repository != repository || reopened.SourceBranch != branch {
		t.Fatalf("reopened review = %+v, want the same message and branch pair in %s", reopened, repository)
	}
	if reopened.ReviewID == abandoned.ReviewID {
		t.Fatalf("reopened review reuses the closed review's id %s", abandoned.ReviewID)
	}
	if reopened.Status != model.ReviewStatusOpen {
		t.Fatalf("reopened review status = %q, want %q", reopened.Status, model.ReviewStatusOpen)
	}

	// The closed review is still there, still CLOSED: releasing the name is not
	// a delete, and the history a reader finds under it must survive.
	stored, err := reviews.Get(ctx, abandoned.ReviewID)
	mustNoErr(t, err, "read the closed review back")
	if stored.Status != model.ReviewStatusClosed || stored.Name != message {
		t.Fatalf("closed review = %+v, want it retained as CLOSED under its own name", stored)
	}
}

// TestReviewMergeQueueIsPerRepository is the queue half of the same failure:
// a target branch is not what names a queue, so a repository's own queued
// reviews are only ever found by naming its identity too.
func TestReviewMergeQueueIsPerRepository(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	queue := func(repository, sourceBranch string) model.Review {
		review, err := reviews.Create(ctx, model.Review{
			Repository: repository, Name: "Land " + sourceBranch,
			TargetBranch: "main", SourceBranch: sourceBranch, Status: model.ReviewStatusOpen,
		})
		mustNoErr(t, err, "create review "+sourceBranch)
		build, err := builds.Create(ctx, model.Build{
			ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: sourceBranch + "-sha", Version: "1.0.0",
		})
		mustNoErr(t, err, "create build "+sourceBranch)
		review.Status = model.ReviewStatusReady
		review.LastReadyBuildID = build.BuildID
		review, err = reviews.Update(ctx, review)
		mustNoErr(t, err, "mark "+sourceBranch+" READY")
		_, err = reviews.CreateMergeQueueEntry(ctx, model.ReviewMergeQueueEntry{TargetBranch: review.TargetBranch, ReviewID: review.ReviewID})
		mustNoErr(t, err, "queue "+sourceBranch)
		return review
	}

	ours := queue("https://github.com/sophium/erun", "feature/ours")
	theirs := queue("https://github.com/sophium/other", "feature/theirs")

	listed, err := reviews.ListMergeQueue(ctx, "https://github.com/sophium/erun", "main")
	mustNoErr(t, err, "list our repository's queue")
	if len(listed) != 1 || listed[0].ReviewID != ours.ReviewID {
		t.Fatalf("ListMergeQueue(erun) = %+v, want exactly %s — the other repository's review %s shares the target branch, not the queue",
			listed, ours.ReviewID, theirs.ReviewID)
	}

	head, err := reviews.FindNextMergeQueueReview(ctx, "https://github.com/sophium/erun", "main")
	mustNoErr(t, err, "find our queue head")
	if head.ReviewID != ours.ReviewID {
		t.Fatalf("our queue head = %s, want %s", head.ReviewID, ours.ReviewID)
	}

	if _, err := reviews.FindActiveMergeReview(ctx, "https://github.com/sophium/other", "main"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the other repository reports a merging review: err = %v, want ErrNotFound", err)
	}

	repositories, err := reviews.QueuedRepositories(ctx, "main")
	mustNoErr(t, err, "read the branch's queued repositories")
	if len(repositories) != 2 {
		t.Fatalf("QueuedRepositories(main) = %v, want both repositories — an unfiltered promotion over these is the ambiguity the platform must refuse", repositories)
	}
}
