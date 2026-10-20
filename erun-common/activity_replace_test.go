package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An idle-status read parses every activity snapshot and reclaims every lease
// it cannot parse, while the desktop, the CLI and the MCP server rewrite those
// same files. A write that truncates the file a reader is reading hands it an
// empty or half-written document. These tests hold a hard link to the inode a
// reader would have open across a write: a write that replaces the file leaves
// that inode whole, and one that rewrites it in place does not.

func linkObserver(t *testing.T, path string) string {
	t.Helper()
	observer := path + ".observer"
	if err := os.Link(path, observer); err != nil {
		t.Fatalf("link %s: %v", path, err)
	}
	return observer
}

func readFileOrFail(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestRecordEnvironmentActivityReplacesTheSnapshotRatherThanRewritingItInPlace(t *testing.T) {
	isolateActivityCache(t)
	record := func(address string) {
		t.Helper()
		if err := RecordEnvironmentActivity(EnvironmentActivityParams{
			Tenant: "acme", Environment: "dev", Kind: ActivityKindCLI,
			ClientUpdates: []EnvironmentActivityClientUpdate{{Address: address, Bytes: 1}},
		}); err != nil {
			t.Fatalf("record activity: %v", err)
		}
	}
	record("10.0.0.1")
	dir, err := EnvironmentActivityDir("acme", "dev")
	if err != nil {
		t.Fatalf("activity dir: %v", err)
	}
	path := filepath.Join(dir, ActivityKindCLI+".json")
	before := readFileOrFail(t, path)
	observer := linkObserver(t, path)

	record("10.0.0.2")

	if held := readFileOrFail(t, observer); !bytes.Equal(held, before) {
		t.Fatalf("the write rewrote the snapshot a concurrent reader holds in place:\nbefore: %s\nreader now sees: %s", before, held)
	}
	if after := readFileOrFail(t, path); bytes.Equal(after, before) {
		t.Fatalf("expected the snapshot to carry the second client, still %s", after)
	}
	if _, err := LoadEnvironmentActivity("acme", "dev"); err != nil {
		t.Fatalf("the replaced snapshot must parse: %v", err)
	}
}

func TestRenewingALeaseReplacesItsFileRatherThanRewritingItInPlace(t *testing.T) {
	isolateActivityCache(t)
	take := func(now time.Time) {
		t.Helper()
		if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
			Tenant: "acme", Environment: "dev", ID: "job-renew", Name: "renewing job", Now: now,
		}); err != nil {
			t.Fatalf("take lease: %v", err)
		}
	}
	start := time.Now()
	take(start)
	dir, err := environmentActivityLeaseDir("acme", "dev")
	if err != nil {
		t.Fatalf("lease dir: %v", err)
	}
	path := filepath.Join(dir, "job-renew.json")
	before := readFileOrFail(t, path)
	observer := linkObserver(t, path)

	take(start.Add(time.Minute))

	if held := readFileOrFail(t, observer); !bytes.Equal(held, before) {
		t.Fatalf("the renewal rewrote the lease a concurrent reader holds in place:\nbefore: %s\nreader now sees: %s", before, held)
	}
	if err := os.Remove(observer); err != nil {
		t.Fatalf("remove observer: %v", err)
	}
	leases, err := LoadEnvironmentActivityLeases("acme", "dev", start.Add(time.Minute))
	if err != nil {
		t.Fatalf("load leases: %v", err)
	}
	if len(leases) != 1 || leases[0].ID != "job-renew" || !leases[0].RenewedAt.Equal(start.Add(time.Minute)) {
		t.Fatalf("expected the one renewed lease, got %+v", leases)
	}
}
