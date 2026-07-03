// Package golden compares actual integration output against a stored
// expectation file.
package golden

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// UpdateEnv is the env var that flips golden mode from compare to write.
const UpdateEnv = "UPDATE_GOLDEN"

// Equal compares actual output against the file at testdata/<name>.txt
// relative to the test source directory. If UPDATE_GOLDEN=1, the file is
// (re)written with the actual output and the test passes.
func Equal(t *testing.T, name, actual string) {
	t.Helper()
	path := pathFor(t, name)

	if os.Getenv(UpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
			t.Fatalf("golden write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\nrun with %s=1 to create it", path, err, UpdateEnv)
	}
	if got, expected := actual, string(want); got != expected {
		t.Errorf("golden mismatch for %s\n--- expected (%s)\n%s--- got\n%s---\nrun with %s=1 to update",
			name, path, expected, got, UpdateEnv)
	}
}

// pathFor keeps each command's goldens beside its own tests under
// testdata/<command>/<name>.txt.
func pathFor(t *testing.T, name string) string {
	t.Helper()
	if !strings.HasSuffix(name, ".txt") {
		name += ".txt"
	}
	for i := 1; i < 16; i++ {
		_, file, _, ok := runtime.Caller(i)
		if !ok {
			break
		}
		if !strings.Contains(file, "/erun-integration/internal/golden/") {
			return filepath.Join(filepath.Dir(file), "testdata", name)
		}
	}
	t.Fatalf("could not resolve testdata path for %s", name)
	return ""
}
