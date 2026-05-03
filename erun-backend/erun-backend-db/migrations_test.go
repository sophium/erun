package erunbackenddb

import "testing"

func TestNormalizeDialectUsesDefaultForOLTPDatabase(t *testing.T) {
	for _, dialect := range []string{"", "default", "postgres", "postgresql", "pgx"} {
		if got := normalizeDialect(dialect); got != "default" {
			t.Fatalf("normalizeDialect(%q) = %q", dialect, got)
		}
	}
}

func TestMigrationNamesUsesDefaultStream(t *testing.T) {
	names, err := migrationNames("default")
	if err != nil {
		t.Fatalf("migrationNames failed: %v", err)
	}
	if len(names) == 0 {
		t.Fatalf("expected default migrations")
	}
}
