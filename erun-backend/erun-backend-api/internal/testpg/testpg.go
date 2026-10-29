// Package testpg gives a test package a real PostgreSQL, migrated with
// erun-backend-db's migrations; missing Docker is a failure, not a skip.
package testpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
)

const (
	postgresImage = "postgres:18.6"
	atlasImage    = "arigaio/atlas:1.3.5-alpine"
	user          = "erun"
	password      = "testpass"
	database      = "erun"
	// pristineDatabase is never written to; FreshURL clones it.
	pristineDatabase = "erun_pristine"
	postgresAlias    = "postgres"
	// baselineVersion is the migration erun-backend-db's migrate.sh marks applied on a non-clean database.
	baselineVersion = "20260503143000"
)

const ExternalURLEnv = "ERUN_TEST_DATABASE_URL"

var (
	once     sync.Once
	url      string
	errStart error
)

func URL(t testing.TB) string {
	t.Helper()
	if external := strings.TrimSpace(os.Getenv(ExternalURLEnv)); external != "" {
		return external
	}
	once.Do(func() { url, errStart = startWithRetry(context.Background(), start, startupRetryBackoff) })
	if errStart != nil {
		t.Fatalf("testpg: %v", errStart)
	}
	return url
}

const startupRetryBackoff = 3 * time.Second

// dockerStartupError marks a Docker-side startup failure before any migration ran,
// which a loaded daemon produces on its own.
type dockerStartupError struct{ err error }

func (e dockerStartupError) Error() string { return e.err.Error() }
func (e dockerStartupError) Unwrap() error { return e.err }

// startWithRetry retries once, only for a Docker startup error; anything else is
// a real defect.
func startWithRetry(ctx context.Context, attempt func(context.Context) (string, error), backoff time.Duration) (string, error) {
	url, err := attempt(ctx)
	var startup dockerStartupError
	if err == nil || !errors.As(err, &startup) {
		return url, err
	}
	if ctx.Err() != nil {
		return "", errors.Join(err, ctx.Err())
	}
	select {
	case <-ctx.Done():
		return "", errors.Join(err, ctx.Err())
	case <-time.After(backoff):
	}
	url, retryErr := attempt(ctx)
	if retryErr != nil {
		return "", fmt.Errorf("%w (a first attempt failed too: %w)", retryErr, err)
	}
	return url, nil
}

func start(ctx context.Context) (string, error) {
	// Every image is public; skipping credential lookup avoids a credential helper
	// (e.g. macOS keychain) blocking on an invisible prompt.
	if _, set := os.LookupEnv("DOCKER_AUTH_CONFIG"); !set {
		_ = os.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{}}`)
	}
	net, err := network.New(ctx)
	if err != nil {
		return "", dockerStartupError{fmt.Errorf("create a Docker network (the database suites need a Docker daemon): %w", err)}
	}
	pg, err := postgres.Run(ctx, postgresImage,
		postgres.WithDatabase(database), postgres.WithUsername(user), postgres.WithPassword(password),
		postgres.BasicWaitStrategies(),
		network.WithNetwork([]string{postgresAlias}, net),
	)
	if err != nil {
		return "", dockerStartupError{fmt.Errorf("start %s: %w", postgresImage, err)}
	}
	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", err
	}
	internal := fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", user, password, postgresAlias, database)
	if err := migrate(ctx, net, internal); err != nil {
		return "", err
	}
	if err := adminExec(ctx, url, fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, pristineDatabase, database)); err != nil {
		return "", fmt.Errorf("keep a pristine migrated copy: %w", err)
	}
	return url, nil
}

// FreshURL returns a per-test clone of the pristine database, for tests whose
// assertions span every tenant.
func FreshURL(t testing.TB) string {
	t.Helper()
	base := URL(t)
	ctx := context.Background()
	name := fmt.Sprintf("erun_fresh_%d", time.Now().UnixNano())
	create := fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, name, pristineDatabase)
	var err error
	// Two clones of one template at the same instant collide (55006).
	for deadline := time.Now().Add(30 * time.Second); ; {
		if err = adminExec(ctx, base, create); err == nil || !strings.Contains(err.Error(), "55006") || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("testpg: create a fresh database: %v", err)
	}
	t.Cleanup(func() {
		if err := adminExec(ctx, base, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
			t.Logf("testpg: drop %s: %v", name, err)
		}
	})
	parsed, err := neturl.Parse(base)
	if err != nil {
		t.Fatalf("testpg: %v", err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func adminExec(ctx context.Context, databaseURL, statement string) error {
	parsed, err := neturl.Parse(databaseURL)
	if err != nil {
		return err
	}
	parsed.Path = "/postgres"
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, statement)
	return err
}

// migrate applies migrations with Atlas, baselining a non-clean database the way
// erun-backend-db's migrator does.
func migrate(ctx context.Context, net *testcontainers.DockerNetwork, url string) error {
	run, err := testcontainers.Run(ctx, atlasImage,
		testcontainers.WithEntrypoint("sleep", "infinity"),
		network.WithNetwork(nil, net),
	)
	if err != nil {
		return fmt.Errorf("start %s: %w", atlasImage, err)
	}
	defer func() { _ = testcontainers.TerminateContainer(run) }()
	if err := copyAtlasInputs(ctx, run); err != nil {
		return err
	}
	atlas := func(args ...string) (string, error) { return execAtlas(ctx, run, args...) }
	out, err := atlas("migrate", "apply", "--env", "default", "--url", url)
	if err == nil {
		return nil
	}
	if !strings.Contains(out, "connected database is not clean") {
		return fmt.Errorf("apply erun-backend-db migrations: %w\n%s", err, out)
	}
	if out, err := atlas("migrate", "set", baselineVersion, "--env", "default", "--url", url); err != nil {
		return fmt.Errorf("baseline erun-backend-db migrations: %w\n%s", err, out)
	}
	if out, err := atlas("migrate", "apply", "--env", "default", "--url", url); err != nil {
		return fmt.Errorf("apply erun-backend-db migrations: %w\n%s", err, out)
	}
	return nil
}

// copyAtlasInputs skips the module's agent guidance (a symlinked CLAUDE.md).
func copyAtlasInputs(ctx context.Context, run testcontainers.Container) error {
	code, _, err := run.Exec(ctx, []string{"mkdir", "-p", "/work"})
	if err != nil {
		return fmt.Errorf("prepare /work in %s: exit %d: %w", atlasImage, code, err)
	}
	if code != 0 {
		return fmt.Errorf("prepare /work in %s: exit %d: <nil>", atlasImage, code)
	}
	if err := run.CopyFileToContainer(ctx, filepath.Join(dbModuleDir(), "atlas.hcl"), "/work/atlas.hcl", 0o644); err != nil {
		return fmt.Errorf("copy atlas.hcl into %s: %w", atlasImage, err)
	}
	for _, dir := range []string{"migrations", "schema"} {
		if err := run.CopyDirToContainer(ctx, filepath.Join(dbModuleDir(), dir), "/work/"+dir, 0o755); err != nil {
			return fmt.Errorf("copy %s into %s: %w", dir, atlasImage, err)
		}
	}
	return nil
}

func execAtlas(ctx context.Context, run testcontainers.Container, args ...string) (string, error) {
	cmd := append([]string{"sh", "-c", `cd /work && exec atlas "$@"`, "atlas"}, args...)
	code, reader, err := run.Exec(ctx, cmd, tcexec.Multiplexed())
	var out string
	if reader != nil {
		data, _ := io.ReadAll(reader)
		out = string(data)
	}
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("atlas exited %d", code)
	}
	return out, nil
}

func dbModuleDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "erun-backend-db")
}
