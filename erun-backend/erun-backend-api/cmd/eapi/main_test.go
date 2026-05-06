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
