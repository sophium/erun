package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	_ "modernc.org/sqlite"
)

func TestListMergeQueueAllowsAllTargetBranches(t *testing.T) {
	db := openReviewTestDB(t)
	repo := NewReviewRepository(NewTxManager(db, DialectSQLite))
	tenantID := "019abcde-0000-7000-8000-000000000101"
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID})

	insertQueuedReview(t, db, tenantID, "review-main", "main", 1)
	insertQueuedReview(t, db, tenantID, "review-release", "release", 2)

	reviews, err := repo.ListMergeQueue(ctx, "")
	if err != nil {
		t.Fatalf("ListMergeQueue failed: %v", err)
	}
	if len(reviews) != 2 || reviews[0].TargetBranch != "main" || reviews[1].TargetBranch != "release" {
		t.Fatalf("unexpected merge queue: %+v", reviews)
	}
	if reviews[0].CreatedAt.IsZero() || reviews[1].UpdatedAt.IsZero() {
		t.Fatalf("expected SQLite timestamp text to scan, got %+v", reviews)
	}
}

func TestListMergeQueueFiltersTargetBranch(t *testing.T) {
	db := openReviewTestDB(t)
	repo := NewReviewRepository(NewTxManager(db, DialectSQLite))
	tenantID := "019abcde-0000-7000-8000-000000000102"
	ctx := security.WithContext(context.Background(), security.Context{TenantID: tenantID})

	insertQueuedReview(t, db, tenantID, "review-main", "main", 1)
	insertQueuedReview(t, db, tenantID, "review-release", "release", 2)

	reviews, err := repo.ListMergeQueue(ctx, "release")
	if err != nil {
		t.Fatalf("ListMergeQueue failed: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ReviewID != "review-release" {
		t.Fatalf("unexpected merge queue: %+v", reviews)
	}
}

func openReviewTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if _, err := db.Exec(`
		CREATE TABLE reviews (
		  review_id TEXT PRIMARY KEY,
		  tenant_id TEXT NOT NULL,
		  name TEXT NOT NULL,
		  target_branch TEXT NOT NULL,
		  source_branch TEXT NOT NULL,
		  status TEXT NOT NULL,
		  last_failed_build_id TEXT,
		  last_ready_build_id TEXT,
		  last_merged_build_id TEXT,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  UNIQUE (tenant_id, target_branch, review_id)
		);
		CREATE TABLE review_merge_queue (
		  review_merge_queue_id INTEGER PRIMARY KEY,
		  tenant_id TEXT NOT NULL,
		  target_branch TEXT NOT NULL,
		  review_id TEXT NOT NULL,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("create review schema failed: %v", err)
	}
	return db
}

func insertQueuedReview(t *testing.T, db *sql.DB, tenantID, reviewID, targetBranch string, queueID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO reviews (review_id, tenant_id, name, target_branch, source_branch, status, last_ready_build_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, reviewID, tenantID, reviewID, targetBranch, "feature", model.ReviewStatusReady, "build-"+reviewID); err != nil {
		t.Fatalf("insert review failed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO review_merge_queue (review_merge_queue_id, tenant_id, target_branch, review_id)
		VALUES (?, ?, ?, ?)
	`, queueID, tenantID, targetBranch, reviewID); err != nil {
		t.Fatalf("insert queue failed: %v", err)
	}
}
