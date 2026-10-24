package backendapi

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"
)

// Proves schema/*.sql and migrations/default build the same database. atlas migrate
// diff needs Atlas Cloud login for PL/pgSQL functions, so both sides are applied and
// compared by name-ordered SQL introspection, which ignores column order.

const (
	schemaDriftDBModuleDir     = "../erun-backend-db"
	schemaDriftIntrospectFile  = "testdata/schema_drift_introspect.sql"
	schemaDriftContainerModule = "/work/erun-backend-db"
	schemaDriftContainerSQL    = "/work/introspect.sql"
	schemaDriftDeclaredSchema  = "/tmp/declared_schema.sql"
)

// schemaDriftConcatScript reads atlas.hcl's src list so ordering matches atlas.
const schemaDriftConcatScript = `set -eu
cd "$1"
: >"$2"
for f in $(grep -oE 'file://schema/[^"]+\.sql' atlas.hcl | sed 's#file://##'); do
    cat "$f" >>"$2"
    echo >>"$2"
done
[ -s "$2" ]`

func TestSchemaDriftDeclaredMatchesMigrations(t *testing.T) {
	ctx := context.Background()
	migratedURL := testpg.FreshURL(t)
	psql := startRetentionPsql(t, ctx, migratedURL)
	copySchemaDriftInputs(t, ctx, psql)

	declaredURL := createSchemaDriftDeclaredDatabase(t, ctx, psql)
	applySchemaDriftDeclared(t, ctx, psql, declaredURL)

	declared := introspectSchemaDrift(t, ctx, psql, declaredURL)
	migrated := introspectSchemaDrift(t, ctx, psql, psql.url)
	if diff, drifted := schemaDrift(t, ctx, psql, declared, migrated); drifted {
		t.Fatalf("schema/*.sql (declared, atlas.hcl's source of truth) and migrations/default/*.sql (applied) describe different databases:\n\n%s\n"+
			"A '+' line exists only in the migrations -- add it to the matching schema/*.sql file.\n"+
			"A '-' line exists only in the declared schema -- add a migration that applies it, or the declared object was never really shipped.",
			diff)
	}
	t.Log("OK: the declared schema (schema/*.sql via atlas.hcl's src order) and the migration-applied state describe the same database")
}

func TestSchemaDriftComparatorReportsDivergence(t *testing.T) {
	ctx := context.Background()
	psql := startRetentionPsql(t, ctx, testpg.URL(t))
	declared := "### TABLE GRANTS\nai_sessions|erun_tenant|SELECT\n"
	migrated := declared + "ai_sessions|erun_tenant|DELETE\n"

	if diff, drifted := schemaDrift(t, ctx, psql, declared, declared); drifted {
		t.Fatalf("identical introspections reported as drift:\n%s", diff)
	}
	diff, drifted := schemaDrift(t, ctx, psql, declared, migrated)
	if !drifted || !strings.Contains(diff, "\n+ai_sessions|erun_tenant|DELETE\n") {
		t.Fatalf("migration-only grant not reported as a '+' line (drifted=%t):\n%s", drifted, diff)
	}
	diff, drifted = schemaDrift(t, ctx, psql, migrated, declared)
	if !drifted || !strings.Contains(diff, "\n-ai_sessions|erun_tenant|DELETE\n") {
		t.Fatalf("declared-only grant not reported as a '-' line (drifted=%t):\n%s", drifted, diff)
	}
}

func copySchemaDriftInputs(t *testing.T, ctx context.Context, psql retentionPsql) {
	t.Helper()
	if out, code := psql.run(t, ctx, nil, "mkdir", "-p", schemaDriftContainerModule); code != 0 {
		t.Fatalf("prepare the psql container (exit %d):\n%s", code, out)
	}
	module, err := filepath.Abs(schemaDriftDBModuleDir)
	mustNoErr(t, err, "resolve the erun-backend-db path")
	mustNoErr(t, psql.container.CopyFileToContainer(ctx, filepath.Join(module, "atlas.hcl"), schemaDriftContainerModule+"/atlas.hcl", 0o644), "copy atlas.hcl")
	mustNoErr(t, psql.container.CopyDirToContainer(ctx, filepath.Join(module, "schema"), schemaDriftContainerModule+"/schema", 0o755), "copy schema/")
	query, err := filepath.Abs(schemaDriftIntrospectFile)
	mustNoErr(t, err, "resolve the introspection query path")
	mustNoErr(t, psql.container.CopyFileToContainer(ctx, query, schemaDriftContainerSQL, 0o644), "copy the introspection query")
}

func createSchemaDriftDeclaredDatabase(t *testing.T, ctx context.Context, psql retentionPsql) string {
	t.Helper()
	name := fmt.Sprintf("erun_declared_%d", time.Now().UnixNano())
	admin := schemaDriftDatabaseURL(t, psql.url, "postgres")
	if out, code := psql.run(t, ctx, nil, "psql", "-v", "ON_ERROR_STOP=1", "-d", admin, "-c", fmt.Sprintf("CREATE DATABASE %q;", name)); code != 0 {
		t.Fatalf("create %s (exit %d):\n%s", name, code, out)
	}
	t.Cleanup(func() {
		drop := fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE);", name)
		if out, code := psql.run(t, ctx, nil, "psql", "-d", admin, "-c", drop); code != 0 {
			t.Logf("drop %s (exit %d):\n%s", name, code, out)
		}
	})
	return schemaDriftDatabaseURL(t, psql.url, name)
}

func schemaDriftDatabaseURL(t *testing.T, databaseURL, name string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	mustNoErr(t, err, "parse the database URL")
	parsed.Path = "/" + name
	return parsed.String()
}

func applySchemaDriftDeclared(t *testing.T, ctx context.Context, psql retentionPsql, declaredURL string) {
	t.Helper()
	if out, code := psql.run(t, ctx, nil, "sh", "-c", schemaDriftConcatScript, "concat", schemaDriftContainerModule, schemaDriftDeclaredSchema); code != 0 {
		t.Fatalf("no schema/*.sql entries found in atlas.hcl -- src list parsing is broken (exit %d):\n%s", code, out)
	}
	apply := `psql -v ON_ERROR_STOP=1 -d "$1" <"$2"`
	if out, code := psql.run(t, ctx, nil, "sh", "-c", apply, "apply", declaredURL, schemaDriftDeclaredSchema); code != 0 {
		t.Fatalf("%s\napplying schema/*.sql in atlas.hcl's declared order failed -- likely a src ordering bug (a file referencing an object a later file creates)", out)
	}
}

func introspectSchemaDrift(t *testing.T, ctx context.Context, psql retentionPsql, databaseURL string) string {
	t.Helper()
	script := `psql -v ON_ERROR_STOP=1 -d "$1" <"$2"`
	out, code := psql.run(t, ctx, nil, "sh", "-c", script, "introspect", databaseURL, schemaDriftContainerSQL)
	if code != 0 {
		t.Fatalf("introspect %s (exit %d):\n%s", databaseURL, code, out)
	}
	return out
}

func schemaDrift(t *testing.T, ctx context.Context, psql retentionPsql, declared, migrated string) (string, bool) {
	t.Helper()
	declaredFile, migratedFile := "/tmp/declared.txt", "/tmp/migrated.txt"
	mustNoErr(t, psql.container.CopyToContainer(ctx, []byte(declared), declaredFile, 0o644), "write the declared introspection")
	mustNoErr(t, psql.container.CopyToContainer(ctx, []byte(migrated), migratedFile, 0o644), "write the migrated introspection")
	out, code := psql.run(t, ctx, nil, "diff", "-u", declaredFile, migratedFile)
	if code > 1 {
		t.Fatalf("diff exited %d:\n%s", code, out)
	}
	return out, code == 1
}
