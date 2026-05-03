package main

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

func TestInferDialectRecognizesPostgresURLs(t *testing.T) {
	if got := inferDatabase("postgres://erun@example/erun"); got != repository.DialectPostgres {
		t.Fatalf("expected postgres database, got %q", got)
	}
	if got := inferDatabase("postgresql://erun@example/erun"); got != repository.DialectPostgres {
		t.Fatalf("expected postgres database, got %q", got)
	}
}

func TestOpenDatabaseRejectsNonPostgresURL(t *testing.T) {
	_, err := openDatabase("file:erun.db")
	if err == nil || !strings.Contains(err.Error(), "database URL must be PostgreSQL") {
		t.Fatalf("expected postgres URL error, got %v", err)
	}
}

func TestOpenDatabaseRequiresURL(t *testing.T) {
	_, err := openDatabase("")
	if err == nil || !strings.Contains(err.Error(), "database URL is required") {
		t.Fatalf("expected required URL error, got %v", err)
	}
}

func TestConfigFromEnvLoadsAWSIdentityStoreConfig(t *testing.T) {
	t.Setenv("ERUN_AWS_IDENTITY_STORE_ID", "d-1234567890")
	t.Setenv("ERUN_AWS_IDENTITY_STORE_REGION", "eu-west-2")

	cfg := configFromEnv()

	if cfg.AWSIdentityStoreID != "d-1234567890" {
		t.Fatalf("unexpected identity store id: %q", cfg.AWSIdentityStoreID)
	}
	if cfg.AWSIdentityStoreRegion != "eu-west-2" {
		t.Fatalf("unexpected identity store region: %q", cfg.AWSIdentityStoreRegion)
	}
}
