package records

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// A hard link keeps a reader's inode across a write: replacing leaves it whole,
// rewriting in place does not.

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
		if err := RecordEnvironmentActivity(model.EnvironmentActivityParams{
			Tenant: "acme", Environment: "dev", Kind: config.ActivityKindCLI,
			ClientUpdates: []model.EnvironmentActivityClientUpdate{{Address: address, Bytes: 1}},
		}); err != nil {
			t.Fatalf("record activity: %v", err)
		}
	}
	record("10.0.0.1")
	dir, err := activity.EnvironmentActivityDir("acme", "dev")
	if err != nil {
		t.Fatalf("activity dir: %v", err)
	}
	path := filepath.Join(dir, config.ActivityKindCLI+".json")
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
