package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	_ "modernc.org/sqlite"
)

func TestResolveIdentityAutoEnrollsFirstUserForKnownTenantIssuer(t *testing.T) {
	db := openIdentityTestDB(t)
	repo := NewIdentityRepository(db, DialectSQLite)
	ctx := context.Background()
	tenantID := "019abcde-0000-7000-8000-000000000001"
	issuer := "https://sts.aws.example"

	if _, err := db.ExecContext(ctx, `INSERT INTO tenants (tenant_id, name, type) VALUES (?, ?, ?)`, tenantID, "erun", model.TenantTypeCompany); err != nil {
		t.Fatalf("insert tenant failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_issuers (tenant_id, issuer) VALUES (?, ?)`, tenantID, issuer); err != nil {
		t.Fatalf("insert tenant issuer failed: %v", err)
	}

	tenant, user, err := repo.ResolveIdentity(ctx, security.Claims{
		Issuer:   issuer,
		Subject:  "subject-1",
		Username: "Rihards",
	})
	if err != nil {
		t.Fatalf("ResolveIdentity failed: %v", err)
	}
	if tenant.TenantID != tenantID || user.TenantID != tenantID || user.Username != "Rihards" {
		t.Fatalf("unexpected identity: tenant=%+v user=%+v", tenant, user)
	}

	var externalCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_external_ids WHERE tenant_id = ? AND issuer = ? AND external_id = ?`, tenantID, issuer, "subject-1").Scan(&externalCount); err != nil {
		t.Fatalf("query external ids failed: %v", err)
	}
	if externalCount != 1 {
		t.Fatalf("expected external id mapping, got %d", externalCount)
	}
	var roleCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_roles WHERE tenant_id = ? AND user_id = ?`, tenantID, user.UserID).Scan(&roleCount); err != nil {
		t.Fatalf("query roles failed: %v", err)
	}
	if roleCount != 2 {
		t.Fatalf("expected read and write roles, got %d", roleCount)
	}
}

func TestResolveIdentityAutoEnrollsFirstIdentityWithSQLiteTimestampText(t *testing.T) {
	db := openIdentityTestDB(t)
	repo := NewIdentityRepository(db, DialectSQLite)
	ctx := context.Background()
	issuer := "https://sts.aws.example"
	logs := captureIdentityLogs(t)

	tenant, user, err := repo.ResolveIdentity(ctx, security.Claims{
		Issuer:   issuer,
		Subject:  "subject-1",
		Username: "Rihards",
	})
	if err != nil {
		t.Fatalf("ResolveIdentity failed: %v", err)
	}
	if tenant.TenantID == "" || tenant.Name != "operations" || tenant.Type != model.TenantTypeOperations {
		t.Fatalf("unexpected tenant: %+v", tenant)
	}
	if tenant.CreatedAt.IsZero() || tenant.UpdatedAt.IsZero() || user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps to scan from SQLite text values, tenant=%+v user=%+v", tenant, user)
	}
	if got := logs.String(); !strings.Contains(got, "erun api identity enrolled first tenant/user") || !strings.Contains(got, `issuer="https://sts.aws.example"`) || !strings.Contains(got, `subject="subject-1"`) {
		t.Fatalf("expected first identity enrollment log, got %q", got)
	}
}

func TestResolveIdentityDoesNotAutoEnrollSecondUnknownUserForTenant(t *testing.T) {
	db := openIdentityTestDB(t)
	repo := NewIdentityRepository(db, DialectSQLite)
	ctx := context.Background()
	tenantID := "019abcde-0000-7000-8000-000000000002"
	issuer := "https://sts.aws.example"

	if _, err := db.ExecContext(ctx, `INSERT INTO tenants (tenant_id, name, type) VALUES (?, ?, ?)`, tenantID, "erun", model.TenantTypeCompany); err != nil {
		t.Fatalf("insert tenant failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_issuers (tenant_id, issuer) VALUES (?, ?)`, tenantID, issuer); err != nil {
		t.Fatalf("insert tenant issuer failed: %v", err)
	}
	if _, _, err := repo.ResolveIdentity(ctx, security.Claims{Issuer: issuer, Subject: "subject-1", Username: "First"}); err != nil {
		t.Fatalf("first ResolveIdentity failed: %v", err)
	}

	_, _, err := repo.ResolveIdentity(ctx, security.Claims{Issuer: issuer, Subject: "subject-2", Username: "Second"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for second unknown user, got %v", err)
	}
}

func TestResolveIdentityLogsFirstUserEnrollmentForKnownTenantIssuer(t *testing.T) {
	db := openIdentityTestDB(t)
	repo := NewIdentityRepository(db, DialectSQLite)
	ctx := context.Background()
	tenantID := "019abcde-0000-7000-8000-000000000003"
	issuer := "https://sts.aws.example"

	if _, err := db.ExecContext(ctx, `INSERT INTO tenants (tenant_id, name, type) VALUES (?, ?, ?)`, tenantID, "erun", model.TenantTypeCompany); err != nil {
		t.Fatalf("insert tenant failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_issuers (tenant_id, issuer) VALUES (?, ?)`, tenantID, issuer); err != nil {
		t.Fatalf("insert tenant issuer failed: %v", err)
	}
	logs := captureIdentityLogs(t)

	if _, _, err := repo.ResolveIdentity(ctx, security.Claims{
		Issuer:   issuer,
		Subject:  "subject-1",
		Username: "Rihards",
	}); err != nil {
		t.Fatalf("ResolveIdentity failed: %v", err)
	}

	if got := logs.String(); !strings.Contains(got, "erun api identity enrolled first user") || !strings.Contains(got, `tenant="`+tenantID+`"`) || !strings.Contains(got, `issuer="https://sts.aws.example"`) || !strings.Contains(got, `subject="subject-1"`) {
		t.Fatalf("expected first user enrollment log, got %q", got)
	}
}

func captureIdentityLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	output := log.Writer()
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(output)
		log.SetFlags(flags)
	})
	return &buf
}

func openIdentityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign keys failed: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE tenants (
		  tenant_id TEXT PRIMARY KEY,
		  name TEXT NOT NULL UNIQUE,
		  type TEXT NOT NULL,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE tenant_issuers (
		  tenant_id TEXT NOT NULL,
		  issuer TEXT PRIMARY KEY,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
		  UNIQUE (tenant_id, issuer)
		);
		CREATE TABLE users (
		  user_id TEXT PRIMARY KEY,
		  tenant_id TEXT NOT NULL,
		  username TEXT NOT NULL,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
		  UNIQUE (tenant_id, user_id),
		  UNIQUE (tenant_id, username)
		);
		CREATE TABLE user_external_ids (
		  tenant_id TEXT NOT NULL,
		  user_id TEXT NOT NULL,
		  issuer TEXT NOT NULL,
		  external_id TEXT NOT NULL,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  PRIMARY KEY (tenant_id, issuer, external_id),
		  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id),
		  FOREIGN KEY (tenant_id, issuer) REFERENCES tenant_issuers (tenant_id, issuer)
		);
		CREATE TABLE roles (
		  role_id TEXT PRIMARY KEY,
		  tenant_id TEXT NOT NULL,
		  name TEXT NOT NULL,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
		  UNIQUE (tenant_id, role_id),
		  UNIQUE (tenant_id, name)
		);
		CREATE TABLE role_permissions (
		  role_permission_id TEXT PRIMARY KEY,
		  tenant_id TEXT NOT NULL,
		  role_id TEXT NOT NULL,
		  api_method TEXT,
		  api_path TEXT,
		  api_method_pattern TEXT,
		  api_path_pattern TEXT,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, role_id)
		);
		CREATE TABLE user_roles (
		  tenant_id TEXT NOT NULL,
		  user_id TEXT NOT NULL,
		  role_id TEXT NOT NULL,
		  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  PRIMARY KEY (tenant_id, user_id, role_id),
		  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id),
		  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, role_id)
		);
	`); err != nil {
		t.Fatalf("create identity schema failed: %v", err)
	}
	return db
}
