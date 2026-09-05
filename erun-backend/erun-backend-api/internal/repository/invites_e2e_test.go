package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Invite creation/listing/revocation is RLS-protected like every other
// tenant-owned table, and ConsumeByToken deliberately runs with no
// authenticated security context at all (WithinSystemTx) — both properties
// only mean something against a real migrated PostgreSQL, not a fake that
// agrees with itself.
func invitesDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_INVITES_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_INVITES_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	tenantID := seedInvitesTenant(t, db, "invites-e2e", "COMPANY")
	t.Cleanup(func() { clearInvitesTenant(t, db, tenantID) })
	return db, tenantID
}

func seedInvitesTenant(t *testing.T, db *sql.DB, label, tenantType string) string {
	t.Helper()
	var tenantID string
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING tenant_id`,
		label+"-"+time.Now().Format("20060102150405.000000"), tenantType,
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	return tenantID
}

func clearInvitesTenant(t *testing.T, db *sql.DB, tenantID string) {
	t.Helper()
	for _, table := range []string{"invites", "users", "tenants"} {
		if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
		}
	}
}

func seedInvitesUser(t *testing.T, db *sql.DB, tenantID, username string) string {
	t.Helper()
	var userID string
	err := db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, username,
	).Scan(&userID)
	mustNoErr(t, err, "seed user "+username)
	return userID
}

func invitesContext(tenantID, tenantType, userID string) context.Context {
	return security.WithContext(context.Background(), security.Context{
		TenantID: tenantID, TenantType: tenantType, ErunUserID: userID,
	})
}

// TestInviteCreateListRevoke locks the operator-facing CRUD contract (#1483
// items 2/3): a created invite is listed as outstanding, and revoking it
// both removes it from that list and refuses a second revoke.
func TestInviteCreateListRevoke(t *testing.T) {
	db, tenantID := invitesDatabase(t)
	inviter := seedInvitesUser(t, db, tenantID, "inviter")
	ctx := invitesContext(tenantID, "COMPANY", inviter)
	repo := NewInviteRepository(NewTxManager(db, DialectPostgres))

	invite, err := repo.Create(ctx, CreateInviteParams{Issuer: "https://auth.example.com", Email: "new@example.com", TTL: time.Hour})
	mustNoErr(t, err, "create invite")
	if invite.Token == "" {
		t.Fatal("created invite has no token")
	}
	if invite.CreatedByUserID != inviter {
		t.Fatalf("createdByUserId = %q, want the authenticated caller %q", invite.CreatedByUserID, inviter)
	}

	listed, err := repo.List(ctx, InviteFilter{TenantID: tenantID})
	mustNoErr(t, err, "list invites")
	if len(listed) != 1 || listed[0].InviteID != invite.InviteID {
		t.Fatalf("listed %+v, want exactly the created invite", listed)
	}

	mustNoErr(t, repo.Revoke(ctx, invite.InviteID), "revoke invite")

	listed, err = repo.List(ctx, InviteFilter{TenantID: tenantID})
	mustNoErr(t, err, "list invites after revoke")
	if len(listed) != 0 {
		t.Fatalf("listed %d invites after revoke, want 0", len(listed))
	}

	if err := repo.Revoke(ctx, invite.InviteID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking an already-revoked invite = %v, want ErrNotFound", err)
	}
}

// TestInviteConsumeByTokenIsSingleUse locks #1483's single-use requirement:
// a second accept of the same token is refused, asserted against the
// persisted row (consumed_at set), not just the returned error.
func TestInviteConsumeByTokenIsSingleUse(t *testing.T) {
	db, tenantID := invitesDatabase(t)
	inviter := seedInvitesUser(t, db, tenantID, "inviter")
	ctx := invitesContext(tenantID, "COMPANY", inviter)
	repo := NewInviteRepository(NewTxManager(db, DialectPostgres))

	invite, err := repo.Create(ctx, CreateInviteParams{Issuer: "https://auth.example.com", TTL: time.Hour})
	mustNoErr(t, err, "create invite")

	consumed, err := repo.ConsumeByToken(context.Background(), invite.Token)
	mustNoErr(t, err, "consume invite")
	if consumed.Invite.InviteID != invite.InviteID {
		t.Fatalf("consumed invite id = %q, want %q", consumed.Invite.InviteID, invite.InviteID)
	}
	if consumed.TenantType != "COMPANY" {
		t.Fatalf("consumed tenant type = %q, want COMPANY", consumed.TenantType)
	}

	var consumedAt sql.NullTime
	mustNoErr(t, db.QueryRow(`SELECT consumed_at FROM invites WHERE invite_id = $1`, invite.InviteID).Scan(&consumedAt), "read back consumed_at")
	if !consumedAt.Valid {
		t.Fatal("consumed_at was not persisted")
	}

	if _, err := repo.ConsumeByToken(context.Background(), invite.Token); !errors.Is(err, ErrInviteConsumed) {
		t.Fatalf("second consume = %v, want ErrInviteConsumed", err)
	}
}

// TestInviteConsumeByTokenRefusesExpiredAndUnknownTokens locks the other two
// states #1483 asks to distinguish plainly: an expired invite and one that
// never existed.
func TestInviteConsumeByTokenRefusesExpiredAndUnknownTokens(t *testing.T) {
	db, tenantID := invitesDatabase(t)
	inviter := seedInvitesUser(t, db, tenantID, "inviter")
	ctx := invitesContext(tenantID, "COMPANY", inviter)
	repo := NewInviteRepository(NewTxManager(db, DialectPostgres))

	expired, err := repo.Create(ctx, CreateInviteParams{Issuer: "https://auth.example.com", TTL: -time.Hour})
	mustNoErr(t, err, "create already-expired invite")

	if _, err := repo.ConsumeByToken(context.Background(), expired.Token); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("consuming an expired invite = %v, want ErrInviteExpired", err)
	}

	if _, err := repo.ConsumeByToken(context.Background(), "not-a-real-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consuming an unknown token = %v, want ErrNotFound", err)
	}
}

// TestInviteCreateTargetingAnotherTenantRequiresExplicitOverride locks that
// InviteFilter/CreateInviteParams' TenantID override actually scopes rows to
// the named tenant rather than the caller's own session tenant — the
// primitive resolveTargetTenant's OPERATIONS-only gate builds on at the
// route layer.
func TestInviteCreateTargetingAnotherTenantRequiresExplicitOverride(t *testing.T) {
	db, callerTenantID := invitesDatabase(t)
	otherTenantID := seedInvitesTenant(t, db, "invites-e2e-other", "COMPANY")
	t.Cleanup(func() { clearInvitesTenant(t, db, otherTenantID) })
	inviter := seedInvitesUser(t, db, callerTenantID, "inviter")
	// erun_operations is what makes a cross-tenant explicit TenantID actually
	// land against a different tenant than the caller's own session tenant.
	ctx := invitesContext(callerTenantID, "OPERATIONS", inviter)
	repo := NewInviteRepository(NewTxManager(db, DialectPostgres))

	invite, err := repo.Create(ctx, CreateInviteParams{TenantID: otherTenantID, Issuer: "https://auth.example.com", TTL: time.Hour})
	mustNoErr(t, err, "create invite for another tenant")
	if invite.TenantID != otherTenantID {
		t.Fatalf("invite tenantId = %q, want the targeted tenant %q", invite.TenantID, otherTenantID)
	}

	listedOwn, err := repo.List(ctx, InviteFilter{TenantID: callerTenantID})
	mustNoErr(t, err, "list caller's own tenant")
	if len(listedOwn) != 0 {
		t.Fatalf("caller's own tenant listed %d invites, want 0 (the invite targeted the other tenant)", len(listedOwn))
	}
}
