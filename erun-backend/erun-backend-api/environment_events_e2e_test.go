package backendapi

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// The log's whole contract is positional -- the sequence is the resume
// cursor, and the read is scoped by tenant in SQL rather than by RLS -- so it
// is exercised against a real migrated PostgreSQL. A fake would agree with
// itself about ordering and scoping, which is exactly what is under test.
func environmentEventDatabase(t *testing.T) (*repository.EnvironmentEventRepository, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_ENVIRONMENT_EVENTS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_ENVIRONMENT_EVENTS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	return repository.NewEnvironmentEventRepository(repository.NewTxManager(db, repository.DialectPostgres)), db
}

// seedEnvironmentEventTenant creates a tenant, one of its users, and one
// environment of its own, so a run never disturbs rows another tenant owns and
// RLS is actually exercised. environment_events.tenant_id/environment_id
// foreign-key the environment, so the environment row is required before any
// event can be appended against it.
func seedEnvironmentEventTenant(t *testing.T, db *sql.DB) (tenantID, userID, environmentID string) {
	t.Helper()
	stamp := time.Now().Format("20060102150405.000000")
	err := db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		"env-events-e2e-"+stamp,
	).Scan(&tenantID)
	mustNoErr(t, err, "seed tenant")
	err = db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		tenantID, "env-events-e2e-user-"+stamp,
	).Scan(&userID)
	mustNoErr(t, err, "seed user")
	err = db.QueryRow(
		`INSERT INTO environments (tenant_id, name, type) VALUES ($1, $2, 'local-agent') RETURNING environment_id`,
		tenantID, "env-events-e2e-env-"+stamp,
	).Scan(&environmentID)
	mustNoErr(t, err, "seed environment")
	t.Cleanup(func() {
		// environment_events cascades with its environment; the rest is
		// removed explicitly so a run leaves nothing behind.
		if _, err := db.Exec(`DELETE FROM environments WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's environments: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM users WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant's users: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing the test tenant: %v", err)
		}
	})
	return tenantID, userID, environmentID
}

func environmentEventContext(tenantID, userID string) context.Context {
	return security.WithContext(context.Background(), security.Context{
		TenantID: tenantID, TenantType: "COMPANY", ErunUserID: userID,
	})
}

func appendEnvironmentEvent(t *testing.T, repo *repository.EnvironmentEventRepository, ctx context.Context, environmentID string, kind model.EnvironmentEventKind) model.EnvironmentEvent {
	t.Helper()
	event, err := repo.Append(ctx, model.EnvironmentEvent{EnvironmentID: environmentID, Kind: kind})
	mustNoErr(t, err, "append environment event")
	return event
}

// TestEnvironmentEventReadResumesStrictlyAfterTheCursor is the resume
// contract itself: the position a reader hands back names the event it has
// already seen, so the next page must start past it. An inclusive comparison
// would redeliver that event on every reconnect forever -- a client that
// resumed would never make progress, and the same event would be reported
// twice.
func TestEnvironmentEventReadResumesStrictlyAfterTheCursor(t *testing.T) {
	repo, db := environmentEventDatabase(t)
	tenantID, userID, environmentID := seedEnvironmentEventTenant(t, db)
	ctx := environmentEventContext(tenantID, userID)

	first := appendEnvironmentEvent(t, repo, ctx, environmentID, model.EnvironmentEventStatusChanged)
	second := appendEnvironmentEvent(t, repo, ctx, environmentID, model.EnvironmentEventJobFinished)
	if second.EnvironmentEventSeq <= first.EnvironmentEventSeq {
		t.Fatalf("seq did not advance: first %d, second %d", first.EnvironmentEventSeq, second.EnvironmentEventSeq)
	}

	fromStart, err := repo.Read(ctx, repository.EnvironmentEventFilter{EnvironmentID: environmentID})
	mustNoErr(t, err, "read from the start")
	if len(fromStart.Events) != 2 {
		t.Fatalf("from the start: %d events, want 2", len(fromStart.Events))
	}
	if fromStart.Events[0].EnvironmentEventSeq != first.EnvironmentEventSeq || fromStart.Events[1].EnvironmentEventSeq != second.EnvironmentEventSeq {
		t.Fatalf("events are not oldest-first: %d then %d", fromStart.Events[0].EnvironmentEventSeq, fromStart.Events[1].EnvironmentEventSeq)
	}

	resumed, err := repo.Read(ctx, repository.EnvironmentEventFilter{
		EnvironmentID: environmentID,
		Cursor:        repository.EnvironmentEventCursor{Seq: first.EnvironmentEventSeq},
	})
	mustNoErr(t, err, "read after the first event")
	if len(resumed.Events) != 1 {
		t.Fatalf("after cursor %d: %d events, want 1 (body %+v)", first.EnvironmentEventSeq, len(resumed.Events), resumed.Events)
	}
	if resumed.Events[0].EnvironmentEventID != second.EnvironmentEventID {
		t.Fatalf("after cursor %d: got %s, want the event that followed it (%s)", first.EnvironmentEventSeq, resumed.Events[0].EnvironmentEventID, second.EnvironmentEventID)
	}

	atEnd, err := repo.Read(ctx, repository.EnvironmentEventFilter{
		EnvironmentID: environmentID,
		Cursor:        repository.EnvironmentEventCursor{Seq: second.EnvironmentEventSeq},
	})
	mustNoErr(t, err, "read after the last event")
	if len(atEnd.Events) != 0 {
		t.Fatalf("at the end: %d events, want 0", len(atEnd.Events))
	}
}

// TestEnvironmentEventReadPaginatesWithAStableCursor pins the cursor a page
// hands back: it names the last event the page delivered, so walking pages
// with it reaches every event exactly once -- no skip, no repeat.
func TestEnvironmentEventReadPaginatesWithAStableCursor(t *testing.T) {
	repo, db := environmentEventDatabase(t)
	tenantID, userID, environmentID := seedEnvironmentEventTenant(t, db)
	ctx := environmentEventContext(tenantID, userID)

	const total = 5
	seen := map[string]bool{}
	for i := 0; i < total; i++ {
		seen[appendEnvironmentEvent(t, repo, ctx, environmentID, model.EnvironmentEventJobFinished).EnvironmentEventID] = true
	}

	page, err := repo.Read(ctx, repository.EnvironmentEventFilter{EnvironmentID: environmentID, Limit: 2})
	mustNoErr(t, err, "first page")
	walked := 0
	for len(page.Events) > 0 {
		walked += len(page.Events)
		if page.NextCursor == "" {
			break
		}
		cursor, err := repository.ParseEnvironmentEventCursor(page.NextCursor)
		mustNoErr(t, err, "parse next cursor")
		page, err = repo.Read(ctx, repository.EnvironmentEventFilter{EnvironmentID: environmentID, Cursor: cursor, Limit: 2})
		mustNoErr(t, err, "next page")
	}
	if walked != total {
		t.Fatalf("walked %d events, want %d", walked, total)
	}
}

// TestEnvironmentEventReadScopesToOneEnvironment pins the optional filter: a
// tenant-wide stream narrowed to one environment must not carry another
// environment's events.
func TestEnvironmentEventReadScopesToOneEnvironment(t *testing.T) {
	repo, db := environmentEventDatabase(t)
	tenantID, userID, environmentID := seedEnvironmentEventTenant(t, db)
	ctx := environmentEventContext(tenantID, userID)

	var otherEnvironmentID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO environments (tenant_id, name, type) VALUES ($1, $2, 'local-agent') RETURNING environment_id`,
		tenantID, "env-events-e2e-other-"+time.Now().Format("20060102150405.000000"),
	).Scan(&otherEnvironmentID), "seed second environment")

	appendEnvironmentEvent(t, repo, ctx, environmentID, model.EnvironmentEventStatusChanged)
	appendEnvironmentEvent(t, repo, ctx, otherEnvironmentID, model.EnvironmentEventSessionAwaitingInput)

	narrowed, err := repo.Read(ctx, repository.EnvironmentEventFilter{EnvironmentID: environmentID})
	mustNoErr(t, err, "read one environment")
	if len(narrowed.Events) != 1 {
		t.Fatalf("%d events, want 1", len(narrowed.Events))
	}
	if narrowed.Events[0].EnvironmentID != environmentID {
		t.Fatalf("event belongs to %s, want %s", narrowed.Events[0].EnvironmentID, environmentID)
	}

	tenantWide, err := repo.Read(ctx, repository.EnvironmentEventFilter{})
	mustNoErr(t, err, "read the tenant's whole log")
	if len(tenantWide.Events) != 2 {
		t.Fatalf("tenant-wide read: %d events, want 2", len(tenantWide.Events))
	}
}

// TestEnvironmentEventReadDoesNotLeakAnotherTenantsLog pins the explicit
// tenant_id predicate. RLS alone is not enough here: erun_operations' policy
// is unconditional (USING (true)), so an OPERATIONS caller with no filter
// would otherwise read every tenant's log.
func TestEnvironmentEventReadDoesNotLeakAnotherTenantsLog(t *testing.T) {
	repo, db := environmentEventDatabase(t)
	strangerTenantID, strangerUserID, strangerEnvironmentID := seedEnvironmentEventTenant(t, db)
	appendEnvironmentEvent(t, repo, environmentEventContext(strangerTenantID, strangerUserID), strangerEnvironmentID, model.EnvironmentEventStatusChanged)

	tenantID, userID, environmentID := seedEnvironmentEventTenant(t, db)
	ctx := environmentEventContext(tenantID, userID)
	appendEnvironmentEvent(t, repo, ctx, environmentID, model.EnvironmentEventJobFinished)

	page, err := repo.Read(ctx, repository.EnvironmentEventFilter{})
	mustNoErr(t, err, "read the tenant's whole log")
	if len(page.Events) != 1 {
		t.Fatalf("%d events, want only the caller's own 1", len(page.Events))
	}
	if page.Events[0].EnvironmentID != environmentID {
		t.Fatalf("read an event belonging to %s, want %s", page.Events[0].EnvironmentID, environmentID)
	}
}

// TestEnvironmentEventAppendTakesTheNextPosition pins that the sequence is
// the database's to assign, never the caller's: an Append carrying a position
// must still land at the end of the log rather than at the position it
// claimed, or a caller could rewrite the order every other reader resumes by.
func TestEnvironmentEventAppendTakesTheNextPosition(t *testing.T) {
	repo, db := environmentEventDatabase(t)
	tenantID, userID, environmentID := seedEnvironmentEventTenant(t, db)
	ctx := environmentEventContext(tenantID, userID)

	first := appendEnvironmentEvent(t, repo, ctx, environmentID, model.EnvironmentEventStatusChanged)

	claimed, err := repo.Append(ctx, model.EnvironmentEvent{
		EnvironmentID:       environmentID,
		Kind:                model.EnvironmentEventSessionAwaitingInput,
		EnvironmentEventSeq: 1,
	})
	mustNoErr(t, err, "append carrying a claimed position")
	if claimed.EnvironmentEventSeq <= first.EnvironmentEventSeq {
		t.Fatalf("claimed position won: got seq %d after %d", claimed.EnvironmentEventSeq, first.EnvironmentEventSeq)
	}
}

// TestEnvironmentEventAppendRefusesAnUnknownKind pins the closed vocabulary
// at the database, not only in the route: a kind nothing reports must not be
// storable.
func TestEnvironmentEventAppendRefusesAnUnknownKind(t *testing.T) {
	repo, db := environmentEventDatabase(t)
	tenantID, userID, environmentID := seedEnvironmentEventTenant(t, db)
	ctx := environmentEventContext(tenantID, userID)

	_, err := repo.Append(ctx, model.EnvironmentEvent{EnvironmentID: environmentID, Kind: model.EnvironmentEventKind("something-new")})
	if err == nil {
		t.Fatal("appending an unknown kind succeeded, want a refusal")
	}
}
