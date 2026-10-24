package repository

import (
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

// Legacy GATE rows stay insertable because MERGED reviews still reference them.
func TestBuildKindContractRequiresAVersionAndKeepsLegacyGateRowsValid(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	review := openReview(t, reviews, ctx, "build kind contract review", "feature/build-kind-contract")

	if _, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "merge-sha",
	}); err == nil {
		t.Fatal("a RECORDED build with no version was accepted, want the CHECK constraint to require one")
	}

	recorded, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "merge-sha", Version: "1.0.0",
	})
	mustNoErr(t, err, "create a RECORDED build with a version")
	if recorded.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", recorded.Version)
	}

	failed, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: false, CommitID: "merge-sha-bad", Version: "1.0.1",
		FailureDetail: "erun build failed: go vet found 3 issues",
	})
	mustNoErr(t, err, "create a failed RECORDED build with a failure detail")
	if failed.FailureDetail == "" {
		t.Fatal("failureDetail was not persisted for a failed RECORDED build")
	}
	_, err = builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: false, CommitID: "merge-sha-bad", Version: "1.0.2",
	})
	mustNoErr(t, err, "create a failed RECORDED build with no failure detail")

	legacy, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindGate, Successful: true, CommitID: "legacy-merge-sha",
	})
	mustNoErr(t, err, "insert a historical-shape GATE row with no version")
	read, err := builds.Get(ctx, tenantID, legacy.BuildID)
	mustNoErr(t, err, "read the historical GATE row back")
	if read.Kind != model.BuildKindGate || read.Version != "" {
		t.Fatalf("historical GATE row read back as kind %q version %q, want GATE with no version", read.Kind, read.Version)
	}
	if _, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindGate, Successful: true, CommitID: "legacy-merge-sha", Version: "1.0.0",
	}); err == nil {
		t.Fatal("a GATE row with a version was accepted, want the CHECK constraint to keep the historical shape")
	}
}

func TestBuildListPageFiltersByCommit(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))
	review := openReview(t, reviews, ctx, "commit filter review", "feature/commit-filter")

	landed, err := builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "landed-sha", Version: "1.0.0",
	})
	mustNoErr(t, err, "create the landed commit's successful build")
	_, err = builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: false, CommitID: "landed-sha", Version: "1.0.1",
	})
	mustNoErr(t, err, "create the landed commit's failed build")
	_, err = builds.Create(ctx, model.Build{
		ReviewID: review.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "other-sha", Version: "1.0.2",
	})
	mustNoErr(t, err, "create another commit's build")

	successful := true
	page, err := builds.ListPage(ctx, BuildListFilter{CommitID: "landed-sha", Successful: &successful})
	mustNoErr(t, err, "list the landed commit's successful builds")
	if len(page.Builds) != 1 || page.Builds[0].BuildID != landed.BuildID {
		t.Fatalf("ListPage(commit=landed-sha, successful) = %+v, want exactly build %s", page.Builds, landed.BuildID)
	}

	page, err = builds.ListPage(ctx, BuildListFilter{CommitID: "landed-sha"})
	mustNoErr(t, err, "list every build of the landed commit")
	if len(page.Builds) != 2 {
		t.Fatalf("ListPage(commit=landed-sha) returned %d builds, want both of that commit's builds and not the other commit's", len(page.Builds))
	}
}

// Get uses a hand-written column list, so a missing column would read back as zero.
func TestBuildProfileRoundTripsThroughCreateAndGet(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	ctx := reviewsContext(tenantID, author)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	review := openReview(t, reviews, ctx, "build profile review", "feature/build-profile")

	profile := &modelpkg.BuildProfileSummary{
		DurationSeconds: 42.5,
		TotalStepCount:  2,
		TopSteps: []modelpkg.BuildProfileStepSummary{
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

	fetched, err := builds.Get(ctx, created.TenantID, created.BuildID)
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

func TestBuildTenantIsolation(t *testing.T) {
	db, tenantA := reviewsDatabase(t)
	authorA := seedReviewsUser(t, db, tenantA, "tenant-a-author")
	ctxA := reviewsContext(tenantA, authorA)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))

	reviewA := openReview(t, reviews, ctxA, "tenant a build review", "feature/tenant-a-build")
	buildA, err := builds.Create(ctxA, model.Build{
		ReviewID: reviewA.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "merge-sha-tenant-a", Version: "1.0.0",
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

	if _, err := builds.Get(ctxB, tenantB, buildA.BuildID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant B fetching tenant A's build by ID: err = %v, want ErrNotFound", err)
	}
}

func TestBuildListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	db, strangerTenantID := reviewsDatabase(t)
	strangerAuthor := seedReviewsUser(t, db, strangerTenantID, "stranger-author")
	strangerCtx := reviewsContext(strangerTenantID, strangerAuthor)
	reviews := NewReviewRepository(NewTxManager(db, DialectPostgres))
	builds := NewBuildRepository(NewTxManager(db, DialectPostgres))
	strangerReview := openReview(t, reviews, strangerCtx, "stranger build review", "feature/stranger-build")
	_, err := builds.Create(strangerCtx, model.Build{
		ReviewID: strangerReview.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "merge-sha-stranger", Version: "1.0.0",
	})
	mustNoErr(t, err, "create stranger build")

	opsTenantID := seedReviewsTenantOfType(t, db, "reviews-e2e-ops-build", "OPERATIONS")
	t.Cleanup(func() { clearReviewsTenant(t, db, opsTenantID) })
	opsAuthor := seedReviewsUser(t, db, opsTenantID, "ops-author")
	opsCtx := reviewsContextOfType(opsTenantID, opsAuthor, "OPERATIONS")
	opsReview := openReview(t, reviews, opsCtx, "ops build review", "feature/ops-build")
	opsBuild, err := builds.Create(opsCtx, model.Build{
		ReviewID: opsReview.ReviewID, Kind: model.BuildKindRecorded, Successful: true, CommitID: "merge-sha-ops", Version: "1.0.0",
	})
	mustNoErr(t, err, "create ops build")

	listed, err := builds.List(opsCtx, BuildFilter{})
	mustNoErr(t, err, "list builds as operations caller")
	if len(listed) != 1 || listed[0].BuildID != opsBuild.BuildID {
		t.Fatalf("List = %+v, want exactly the operations caller's own build %s, not the stranger's as well", listed, opsBuild.BuildID)
	}
}
