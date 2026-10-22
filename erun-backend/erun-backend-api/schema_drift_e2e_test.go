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

// Proves that the declarative schema (schema/*.sql, the source of truth
// atlas.hcl's `src` list points at) and the migration-applied state
// (migrations/default) describe the exact same database, against a real
// postgres rather than a catalog-privilege guess. A grant present in a
// migration but missing from schema/roles.sql (or the reverse), a missing
// trigger, a differently-named constraint, or a table on one side only all
// fail here.
//
// `atlas migrate diff` cannot do this comparison: the schema declares
// PL/pgSQL functions, and the pinned atlas CLI (1.2.0) refuses to diff a
// function-bearing source without `atlas login` against Atlas Cloud. So both
// states are built by pure execution -- testpg's `atlas migrate apply` for
// the migrations, and atlas.hcl's own `src` list concatenated and piped
// through psql for the declared side -- then compared with plain SQL
// introspection ordered by name, so incremental-migration column order is
// never mistaken for drift.
//
// Ported from erun-devops/docker/erun-backend-db/schema_drift_test.sh.

const (
	schemaDriftDBModuleDir     = "../erun-backend-db"
	schemaDriftIntrospectFile  = "testdata/schema_drift_introspect.sql"
	schemaDriftContainerModule = "/work/erun-backend-db"
	schemaDriftContainerSQL    = "/work/introspect.sql"
	schemaDriftDeclaredSchema  = "/tmp/declared_schema.sql"
)

// schemaDriftConcatScript is the script's own concatenation of atlas.hcl's
// `src` list, so the ordering can never drift from the one atlas uses.
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
	// The retention suites' psql container, with no policies: only its psql,
	// grep, sed and diff are used.
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

// The comparator must report a one-row divergence, the shape of a grant
// present in a migration but missing from schema/roles.sql.
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

// copySchemaDriftInputs copies atlas.hcl, schema/ and the introspection query
// into the psql container.
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

// createSchemaDriftDeclaredDatabase creates the empty database the declared
// schema is applied to, beside the migrated one, and drops it when the test
// ends.
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

// schemaDriftDatabaseURL is databaseURL pointed at another database.
func schemaDriftDatabaseURL(t *testing.T, databaseURL, name string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	mustNoErr(t, err, "parse the database URL")
	parsed.Path = "/" + name
	return parsed.String()
}

// applySchemaDriftDeclared concatenates schema/*.sql in atlas.hcl's `src`
// order and applies it with psql.
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

// introspectSchemaDrift runs the introspection query against databaseURL and
// returns psql's combined output, the text the two sides are compared by.
func introspectSchemaDrift(t *testing.T, ctx context.Context, psql retentionPsql, databaseURL string) string {
	t.Helper()
	script := `psql -v ON_ERROR_STOP=1 -d "$1" <"$2"`
	out, code := psql.run(t, ctx, nil, "sh", "-c", script, "introspect", databaseURL, schemaDriftContainerSQL)
	if code != 0 {
		t.Fatalf("introspect %s (exit %d):\n%s", databaseURL, code, out)
	}
	return out
}

// schemaDrift compares the two introspections with `diff -u` in the psql
// container and returns the unified diff and whether they differ.
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
