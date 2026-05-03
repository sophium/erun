package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestIdentityBootstrapStatusReportsEmptyDatabase(t *testing.T) {
	db := openIdentityStatusTestDB(t)

	status := identityBootstrapStatus(context.Background(), db)
	if !strings.Contains(status, "identity bootstrap pending") ||
		!strings.Contains(status, "firstTenant=false") ||
		!strings.Contains(status, "firstUser=false") {
		t.Fatalf("unexpected status: %s", status)
	}
}

func TestIdentityBootstrapStatusReportsFirstTenantWithoutUser(t *testing.T) {
	db := openIdentityStatusTestDB(t)
	if _, err := db.Exec(`INSERT INTO tenants (tenant_id, name, type) VALUES ('tenant-1', 'erun', 'COMPANY')`); err != nil {
		t.Fatalf("insert tenant failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer) VALUES ('tenant-1', 'https://issuer.example')`); err != nil {
		t.Fatalf("insert issuer failed: %v", err)
	}

	status := identityBootstrapStatus(context.Background(), db)
	if !strings.Contains(status, "identity bootstrap pending") ||
		!strings.Contains(status, "firstTenant=true") ||
		!strings.Contains(status, "firstUser=false") ||
		!strings.Contains(status, "issuers=1") {
		t.Fatalf("unexpected status: %s", status)
	}
}

func TestIdentityBootstrapStatusReportsFirstUser(t *testing.T) {
	db := openIdentityStatusTestDB(t)
	if _, err := db.Exec(`INSERT INTO tenants (tenant_id, name, type) VALUES ('tenant-1', 'erun', 'COMPANY')`); err != nil {
		t.Fatalf("insert tenant failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tenant_issuers (tenant_id, issuer) VALUES ('tenant-1', 'https://issuer.example')`); err != nil {
		t.Fatalf("insert issuer failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (user_id, tenant_id, username) VALUES ('user-1', 'tenant-1', 'rihards')`); err != nil {
		t.Fatalf("insert user failed: %v", err)
	}

	status := identityBootstrapStatus(context.Background(), db)
	if !strings.Contains(status, "identity ready") ||
		!strings.Contains(status, "firstTenant=true") ||
		!strings.Contains(status, "firstUser=true") ||
		!strings.Contains(status, "users=1") {
		t.Fatalf("unexpected status: %s", status)
	}
}

func openIdentityStatusTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if _, err := db.Exec(`
		CREATE TABLE tenants (
		  tenant_id TEXT PRIMARY KEY,
		  name TEXT NOT NULL,
		  type TEXT NOT NULL
		);
		CREATE TABLE tenant_issuers (
		  tenant_id TEXT NOT NULL,
		  issuer TEXT PRIMARY KEY
		);
		CREATE TABLE users (
		  user_id TEXT PRIMARY KEY,
		  tenant_id TEXT NOT NULL,
		  username TEXT NOT NULL
		);
	`); err != nil {
		t.Fatalf("create schema failed: %v", err)
	}
	return db
}
