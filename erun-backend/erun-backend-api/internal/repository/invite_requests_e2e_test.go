package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// invite_requests has no RLS and no security context to bind (every method
// runs under WithinSystemTx — see InviteRequestRepository's own doc), so its
// one real database-only property is the partial unique index's ON CONFLICT
// upsert behavior, which only means something against real PostgreSQL.
func inviteRequestsDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_INVITE_REQUESTS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_INVITE_REQUESTS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func clearInviteRequestsByIssuer(t *testing.T, db *sql.DB, issuer string) {
	t.Helper()
	if _, err := db.Exec(`DELETE FROM invite_requests WHERE issuer = $1`, issuer); err != nil {
		t.Logf("clearing invite_requests for issuer %s: %v", issuer, err)
	}
}

// TestSubmitUpdatesExistingPendingRequestInsteadOfDuplicating locks the
// issue's §4 abuse bound: one pending request per (issuer, subject),
// enforced by the schema's partial unique index, not only in application
// code. A second submission from the same verified identity updates the
// first request in place — including switching what it asks for — rather
// than queuing a second row.
func TestSubmitUpdatesExistingPendingRequestInsteadOfDuplicating(t *testing.T) {
	db := inviteRequestsDatabase(t)
	issuer := "https://issuer.example.com"
	subject := "resubmit-subject"
	t.Cleanup(func() { clearInviteRequestsByIssuer(t, db, issuer) })
	repo := NewInviteRequestRepository(NewTxManager(db, DialectPostgres))
	ctx := context.Background()

	first, err := repo.Submit(ctx, SubmitInviteRequestParams{
		Issuer: issuer, Subject: subject,
		Kind: model.InviteRequestKindJoinTenant, TenantName: "acme",
	})
	mustNoErr(t, err, "submit first request")
	if first.Status != model.InviteRequestStatusPending {
		t.Fatalf("first request status = %q, want PENDING", first.Status)
	}

	second, err := repo.Submit(ctx, SubmitInviteRequestParams{
		Issuer: issuer, Subject: subject,
		Kind: model.InviteRequestKindCreateTenant, TenantName: "newco", Note: "changed my mind",
	})
	mustNoErr(t, err, "submit second request for the same identity")

	if second.InviteRequestID != first.InviteRequestID {
		t.Fatalf("second submission minted a new row %q, want it to update the first %q", second.InviteRequestID, first.InviteRequestID)
	}
	if second.Kind != model.InviteRequestKindCreateTenant || second.TenantName != "newco" || second.Note != "changed my mind" {
		t.Fatalf("second submission did not update the pending row's fields: %+v", second)
	}

	var count int
	mustNoErr(t, db.QueryRow(`SELECT count(*) FROM invite_requests WHERE issuer = $1 AND subject = $2`, issuer, subject).Scan(&count), "count rows")
	if count != 1 {
		t.Fatalf("row count for (issuer, subject) = %d, want exactly 1", count)
	}
}

// TestMarkApprovedRefusesADecidedRequest locks the atomic PENDING guard:
// once a request has moved past PENDING, a second decision refuses with
// ErrConflict instead of silently re-deciding it — including a resubmission
// after approval, which must create a fresh row again (the partial unique
// index only applies while status = 'PENDING').
func TestMarkApprovedRefusesADecidedRequest(t *testing.T) {
	db := inviteRequestsDatabase(t)
	issuer := "https://issuer.example.com"
	subject := "decided-subject"
	t.Cleanup(func() { clearInviteRequestsByIssuer(t, db, issuer) })
	repo := NewInviteRequestRepository(NewTxManager(db, DialectPostgres))
	ctx := context.Background()

	request, err := repo.Submit(ctx, SubmitInviteRequestParams{
		Issuer: issuer, Subject: subject,
		Kind: model.InviteRequestKindJoinTenant, TenantName: "acme",
	})
	mustNoErr(t, err, "submit request")

	if _, err := repo.MarkDeclined(ctx, request.InviteRequestID, "", "no capacity"); err != nil {
		t.Fatalf("MarkDeclined: %v", err)
	}

	if _, err := repo.MarkApproved(ctx, request.InviteRequestID, "", "invite-does-not-exist"); err == nil {
		t.Fatal("MarkApproved on an already-decided request succeeded, want ErrConflict")
	}

	resubmitted, err := repo.Submit(ctx, SubmitInviteRequestParams{
		Issuer: issuer, Subject: subject,
		Kind: model.InviteRequestKindJoinTenant, TenantName: "acme",
	})
	mustNoErr(t, err, "resubmit after decline")
	if resubmitted.InviteRequestID == request.InviteRequestID {
		t.Fatal("resubmission after a decision reused the decided row instead of creating a fresh pending one")
	}
}
