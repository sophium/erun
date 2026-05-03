package main

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

func TestInferDialectRecognizesPostgresURLs(t *testing.T) {
	if got := inferDialect("postgres://erun@example/erun"); got != repository.DialectPostgres {
		t.Fatalf("expected postgres dialect, got %q", got)
	}
	if got := inferDialect("postgresql://erun@example/erun"); got != repository.DialectPostgres {
		t.Fatalf("expected postgres dialect, got %q", got)
	}
}

func TestOpenDatabaseRejectsSQLite(t *testing.T) {
	_, _, err := openDatabase("file:erun.db", "sqlite")
	if err == nil || !strings.Contains(err.Error(), "unsupported database dialect") {
		t.Fatalf("expected unsupported dialect error, got %v", err)
	}
}

func TestOpenDatabaseRequiresURL(t *testing.T) {
	_, _, err := openDatabase("", "postgres")
	if err == nil || !strings.Contains(err.Error(), "database URL is required") {
		t.Fatalf("expected required URL error, got %v", err)
	}
}
