package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// The thread-identity invariants — one root per (commit, file, line), a
// reply's file must match its root's, only the root's status is settable,
// only the root's own creator may flip it — live in erun_validate_comments,
// so they are exercised against a real migrated PostgreSQL rather than a fake
// that agrees with itself.

// testCommitID is a well-formed 40-character lowercase hex commit ID,
// matching the format CommentService.PrepareCreate requires.
const testCommitID = "0123456789abcdef0123456789abcdef01234567"

type commentsFixture struct {
	db       *sql.DB
	comments *repository.CommentRepository
	service  *service.CommentService
	reviewID string
	alice    context.Context
	bob      context.Context
}

func commentsDatabase(t *testing.T) commentsFixture {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_COMMENTS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_COMMENTS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	suffix := time.Now().Format("20060102150405.000000")
	var tenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"comments-e2e-"+suffix,
	).Scan(&tenantID), "seed tenant")
	t.Cleanup(func() {
		for _, table := range []string{"comments", "reviews", "users", "tenants"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing test tenant rows from %s: %v", table, err)
			}
		}
	})

	var aliceID, bobID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, "alice-"+suffix,
	).Scan(&aliceID), "seed alice")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, "bob-"+suffix,
	).Scan(&bobID), "seed bob")

	alice := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY", ErunUserID: aliceID})
	bob := security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: "COMPANY", ErunUserID: bobID})

	txs := repository.NewTxManager(db, repository.DialectPostgres)
	reviews := repository.NewReviewRepository(txs)
	review, err := reviews.Create(alice, model.Review{Name: "comments e2e", TargetBranch: "main", SourceBranch: "feature", Status: model.ReviewStatusOpen})
	mustNoErr(t, err, "seed review")

	comments := repository.NewCommentRepository(txs)
	return commentsFixture{
		db:       db,
		comments: comments,
		service:  service.NewCommentService(comments),
		reviewID: review.ReviewID,
		alice:    alice,
		bob:      bob,
	}
}

func (f commentsFixture) createRoot(ctx context.Context, filePath string, line int, body string) (model.Comment, error) {
	prepared, err := f.service.PrepareCreate(ctx, model.Comment{
		ReviewID: f.reviewID, CommitID: testCommitID, FilePath: filePath, Line: line, Body: body,
	})
	if err != nil {
		return model.Comment{}, err
	}
	return f.comments.Create(ctx, prepared)
}

func (f commentsFixture) createReply(ctx context.Context, parentCommentID, filePath string, line int, body string) (model.Comment, error) {
	prepared, err := f.service.PrepareCreate(ctx, model.Comment{
		ReviewID: f.reviewID, ParentCommentID: parentCommentID, CommitID: testCommitID, FilePath: filePath, Line: line, Body: body,
	})
	if err != nil {
		return model.Comment{}, err
	}
	return f.comments.Create(ctx, prepared)
}

// TestCommentRootCommentsOnSameLineDifferentFilesBothSucceed is the regression
// from #1198: a comment's identity used to be (commit, line) alone, so a
// second root comment on line 42 of a different file collided with the
// first. Both must now succeed because file_path is part of the key.
func TestCommentRootCommentsOnSameLineDifferentFilesBothSucceed(t *testing.T) {
	f := commentsDatabase(t)

	onA, err := f.createRoot(f.alice, "a.go", 42, "issue on a.go")
	mustNoErr(t, err, "root comment on a.go:42")

	onB, err := f.createRoot(f.alice, "b.go", 42, "issue on b.go")
	mustNoErr(t, err, "root comment on b.go:42 (used to collide with a.go:42)")

	if onA.CommentID == onB.CommentID {
		t.Fatal("expected two distinct comments, got the same row")
	}
}

// TestCommentReplyRecordsOwnAuthor proves a reply is no longer authorless:
// the child's creator_user_id must be the replying user, not empty.
func TestCommentReplyRecordsOwnAuthor(t *testing.T) {
	f := commentsDatabase(t)

	root, err := f.createRoot(f.alice, "c.go", 1, "root comment")
	mustNoErr(t, err, "root comment")

	reply, err := f.createReply(f.bob, root.CommentID, "c.go", 1, "bob's reply")
	mustNoErr(t, err, "bob's reply")

	bobSecurity, _ := security.FromContext(f.bob)
	if reply.CreatorUserID != bobSecurity.ErunUserID {
		t.Fatalf("reply.CreatorUserID = %q, want bob (%q)", reply.CreatorUserID, bobSecurity.ErunUserID)
	}
}

// TestCommentReplyToDifferentFileIsRefused proves a reply resolves to its
// root by the full (commit, file, line) address: a reply whose file
// disagrees with its parent's is refused, not silently attached.
func TestCommentReplyToDifferentFileIsRefused(t *testing.T) {
	f := commentsDatabase(t)

	root, err := f.createRoot(f.alice, "d.go", 5, "root comment")
	mustNoErr(t, err, "root comment")

	_, err = f.createReply(f.bob, root.CommentID, "e.go", 5, "wrong file reply")
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("reply to a different file: err = %v, want ErrInvalidInput", err)
	}
}

// TestCommentOnlyRootCreatorCanCloseThread covers three rules at once: the
// root author can close a thread that contains replies from other users, a
// reply's own status is not separately settable, and a non-creator cannot
// close the root either.
func TestCommentOnlyRootCreatorCanCloseThread(t *testing.T) {
	f := commentsDatabase(t)

	root, err := f.createRoot(f.alice, "f.go", 10, "root comment")
	mustNoErr(t, err, "root comment")
	reply, err := f.createReply(f.bob, root.CommentID, "f.go", 10, "bob's reply")
	mustNoErr(t, err, "bob's reply")

	if _, err := f.service.UpdateStatus(f.bob, reply.CommentID, model.CommentStatusClosed); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("closing a reply's own status: err = %v, want ErrInvalidInput", err)
	}

	if _, err := f.service.UpdateStatus(f.bob, root.CommentID, model.CommentStatusClosed); !errors.Is(err, repository.ErrForbidden) {
		t.Fatalf("non-creator closing the root: err = %v, want ErrForbidden", err)
	}

	closed, err := f.service.UpdateStatus(f.alice, root.CommentID, model.CommentStatusClosed)
	mustNoErr(t, err, "root creator closes a thread with a reply from another user")
	if closed.Status != model.CommentStatusClosed {
		t.Fatalf("status = %q, want CLOSED", closed.Status)
	}
}

// TestCommentCreateRequiresNonEmptyBody proves body validation is enforced
// and surfaces as a client error, not a bare 500.
func TestCommentCreateRequiresNonEmptyBody(t *testing.T) {
	f := commentsDatabase(t)

	if _, err := f.createRoot(f.alice, "g.go", 1, "   "); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("whitespace-only body: err = %v, want ErrInvalidInput", err)
	}
}

// commentsTenantFixture seeds one tenant of tenantType with one user and one
// review, for the cross-tenant scoping regression test below, which needs
// two independent tenants rather than commentsDatabase's single shared one.
func commentsTenantFixture(t *testing.T, db *sql.DB, label, tenantType string) (ctx context.Context, comments *repository.CommentRepository, reviewID string) {
	t.Helper()
	suffix := time.Now().Format("20060102150405.000000")
	var tenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING tenant_id`,
		label+"-"+suffix, tenantType,
	).Scan(&tenantID), "seed tenant")
	t.Cleanup(func() {
		for _, table := range []string{"comments", "reviews", "users", "tenants"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing test tenant rows from %s: %v", table, err)
			}
		}
	})

	var userID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, "user-"+suffix,
	).Scan(&userID), "seed user")
	ctx = security.WithContext(context.Background(), security.Context{TenantID: tenantID, TenantType: tenantType, ErunUserID: userID})

	txs := repository.NewTxManager(db, repository.DialectPostgres)
	reviews := repository.NewReviewRepository(txs)
	review, err := reviews.Create(ctx, model.Review{Name: "comments scope e2e", TargetBranch: "main", SourceBranch: "feature/scope-" + suffix, Status: model.ReviewStatusOpen})
	mustNoErr(t, err, "seed review")

	comments = repository.NewCommentRepository(txs)
	return ctx, comments, review.ReviewID
}

// TestCommentListScopesToTheOperationsCallersOwnTenant pins the failure
// scenario directly: an OPERATIONS caller's List must not include a stranger
// tenant's comments even though erun_operations' RLS policy makes them
// visible too.
func TestCommentListScopesToTheOperationsCallersOwnTenant(t *testing.T) {
	databaseURL := os.Getenv("ERUN_E2E_COMMENTS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_COMMENTS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	strangerCtx, strangerComments, strangerReviewID := commentsTenantFixture(t, db, "comments-e2e-stranger", "COMPANY")
	strangerPrepared, err := service.NewCommentService(strangerComments).PrepareCreate(strangerCtx, model.Comment{
		ReviewID: strangerReviewID, CommitID: testCommitID, FilePath: "stranger.go", Line: 1, Body: "stranger comment",
	})
	mustNoErr(t, err, "prepare stranger comment")
	_, err = strangerComments.Create(strangerCtx, strangerPrepared)
	mustNoErr(t, err, "create stranger comment")

	opsCtx, opsComments, opsReviewID := commentsTenantFixture(t, db, "comments-e2e-ops", "OPERATIONS")
	opsPrepared, err := service.NewCommentService(opsComments).PrepareCreate(opsCtx, model.Comment{
		ReviewID: opsReviewID, CommitID: testCommitID, FilePath: "ops.go", Line: 1, Body: "ops comment",
	})
	mustNoErr(t, err, "prepare ops comment")
	own, err := opsComments.Create(opsCtx, opsPrepared)
	mustNoErr(t, err, "create ops comment")

	listed, err := opsComments.List(opsCtx, repository.CommentFilter{})
	mustNoErr(t, err, "list as operations caller")
	if len(listed) != 1 || listed[0].CommentID != own.CommentID {
		t.Fatalf("List = %+v, want exactly the operations caller's own comment %s, not the stranger's as well", listed, own.CommentID)
	}
}
