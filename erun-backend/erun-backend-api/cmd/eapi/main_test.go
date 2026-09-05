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

// An unenforced audience boundary and one that is merely passing look identical
// in the logs unless the startup line says which it is, so the permissive
// default has to announce itself rather than print a count of zero.
func TestOIDCAudiencePolicySaysWhetherTheBoundaryIsUp(t *testing.T) {
	off := oidcAudiencePolicy(splitCSV(""))
	if off != "oidc audience enforcement=off (any audience from an allowed issuer is accepted)" {
		t.Fatalf("unconfigured policy = %q", off)
	}

	on := oidcAudiencePolicy(splitCSV("console-client, cli-client"))
	if on != "oidc audience enforcement=on (allowed: console-client, cli-client)" {
		t.Fatalf("configured policy = %q", on)
	}
}

// The flag name is a contract with the API image's entrypoint, which translates
// ERUN_OIDC_ALLOWED_AUDIENCES into it; a rename on either side leaves the
// boundary unconfigurable through the chart.
func TestResolveConfigReadsTheOIDCAudienceAllowList(t *testing.T) {
	cfg, err := resolveConfig([]string{"--oidc-allowed-audiences", "console-client,cli-client"})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.AllowedAudiences != "console-client,cli-client" {
		t.Fatalf("allowed audiences = %q", cfg.AllowedAudiences)
	}

	t.Setenv("ERUN_OIDC_ALLOWED_AUDIENCES", "console-client")
	cfg, err = resolveConfig(nil)
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.AllowedAudiences != "console-client" {
		t.Fatalf("allowed audiences from env = %q", cfg.AllowedAudiences)
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
