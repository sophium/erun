package testpg

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var errReaperStartup = dockerStartupError{errors.New(`create a Docker network (the database suites need a Docker daemon): ` +
	`reaper: new reaper: wait for reaper 1a2b3c4d: unexpected container status "created": failed to create network`)}

func scriptedAttempts(results ...error) (func(context.Context) (string, error), *int) {
	calls := 0
	return func(context.Context) (string, error) {
		err := results[calls]
		calls++
		if err != nil {
			return "", err
		}
		return "postgres://ok", nil
	}, &calls
}

func TestStartRetriesADockerStartupFailureOnce(t *testing.T) {
	attempt, calls := scriptedAttempts(errReaperStartup, nil)
	url, err := startWithRetry(context.Background(), attempt, 0)
	if err != nil {
		t.Fatalf("expected the fresh attempt to succeed, got %v", err)
	}
	if url != "postgres://ok" || *calls != 2 {
		t.Fatalf("expected one retry returning the URL, got %q after %d attempts", url, *calls)
	}
}

func TestStartGivesUpAfterTheSecondDockerStartupFailure(t *testing.T) {
	attempt, calls := scriptedAttempts(errReaperStartup, errReaperStartup, nil)
	_, err := startWithRetry(context.Background(), attempt, 0)
	if err == nil || *calls != 2 {
		t.Fatalf("expected exactly two attempts and an error, got %d attempts, err %v", *calls, err)
	}
	if !strings.Contains(err.Error(), "first attempt failed too") {
		t.Fatalf("expected the error to keep the first failure, got %v", err)
	}
}

func TestStartNeverRetriesAMigrationFailure(t *testing.T) {
	migration := errors.New("apply erun-backend-db migrations: atlas exited 1")
	attempt, calls := scriptedAttempts(migration, nil)
	_, err := startWithRetry(context.Background(), attempt, 0)
	if !errors.Is(err, migration) || *calls != 1 {
		t.Fatalf("expected the migration failure after one attempt, got %d attempts, err %v", *calls, err)
	}
}

func TestStartDoesNotRetryOnceTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempt, calls := scriptedAttempts(errReaperStartup, nil)
	_, err := startWithRetry(ctx, attempt, 0)
	if err == nil || *calls != 1 {
		t.Fatalf("expected no retry after cancellation, got %d attempts, err %v", *calls, err)
	}
}
