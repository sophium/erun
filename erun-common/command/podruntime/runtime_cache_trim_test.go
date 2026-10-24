package podruntime

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

func runtimeTestContext(dryRun bool) (core.Context, *bytes.Buffer) {
	var out bytes.Buffer
	return core.Context{
		Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &out, &out),
		DryRun: dryRun,
		Stdout: &out,
		Stderr: &out,
	}, &out
}

// addGoCacheEntry writes one go cache entry the way cmd/go lays it out: a
// two-hex-digit subdirectory holding a data file and its action file, both named
// by the entry's hash, aged by age.
func addGoCacheEntry(t *testing.T, dir string, index, size int, age time.Duration) {
	t.Helper()
	sub := filepath.Join(dir, fmt.Sprintf("%02x", index%256))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%064x", index)
	data := filepath.Join(sub, hash+"-d")
	action := filepath.Join(sub, hash+"-a")
	if err := os.WriteFile(data, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(action, []byte("v1 "+hash+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	for _, path := range []string{data, action} {
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}

func goCacheHasEntry(dir string, index int) bool {
	_, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%02x", index%256), fmt.Sprintf("%064x", index)+"-d"))
	return err == nil
}

func goCacheListing(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	_ = filepath.WalkDir(dir, func(path string, _ os.DirEntry, _ error) error {
		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	return strings.Join(paths, "\n")
}

func mustGoCacheSize(t *testing.T, dir string) int64 {
	t.Helper()
	size, err := goBuildCacheSize(dir)
	if err != nil {
		t.Fatal(err)
	}
	return size
}

// The common case, and the one that decides whether this can run on an
// interval: a cache inside its cap is not touched and nothing is reported.
func TestTrimGoBuildCacheLeavesACacheUnderItsCapAlone(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 16; i++ {
		addGoCacheEntry(t, dir, i, 4096, time.Hour)
	}
	before := goCacheListing(t, dir)
	ctx, out := runtimeTestContext(false)
	result, err := TrimGoBuildCache(ctx, dir, mustGoCacheSize(t, dir)+1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if result.Evicted != 0 || out.Len() != 0 {
		t.Fatalf("a cache under its cap should be silent and untouched, got %+v and %q", result, out.String())
	}
	if after := goCacheListing(t, dir); after != before {
		t.Fatal("a cache under its cap should be untouched")
	}
}

// A cache directory that does not exist holds nothing; creating it would leave
// a build to find a directory it never made.
func TestTrimGoBuildCacheDoesNotCreateAMissingCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	ctx, out := runtimeTestContext(false)
	result, err := TrimGoBuildCache(ctx, dir, 1<<20)
	if err != nil || !result.Missing || out.Len() != 0 {
		t.Fatalf("a missing cache should be a silent no-op, got %+v %v %q", result, err, out.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a missing cache directory must not be created: %v", err)
	}
}

// The point of evicting rather than clearing: the oldest entries go and the
// newest stay.
func TestTrimGoBuildCacheEvictsLeastRecentlyUsedFirst(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		addGoCacheEntry(t, dir, i*100, 200000, time.Duration(400-i*30)*time.Hour)
	}
	limit := mustGoCacheSize(t, dir) / 2
	ctx, out := runtimeTestContext(false)
	if _, err := TrimGoBuildCache(ctx, dir, limit); err != nil {
		t.Fatal(err)
	}
	if size := mustGoCacheSize(t, dir); size > limit {
		t.Fatalf("the cache should be within its %d byte cap, got %d", limit, size)
	}
	if goCacheHasEntry(dir, 0) {
		t.Fatal("the oldest entry should have been evicted")
	}
	if !goCacheHasEntry(dir, 900) {
		t.Fatal("the newest entry should have survived")
	}
	if !strings.Contains(out.String(), "trimmed the go build cache at "+dir) {
		t.Fatalf("a trim that evicted should say so, got %q", out.String())
	}
}

// The cache's bookkeeping sits beside the entries: trim.txt is go's own record
// of its last age trim, and a file in a subdirectory that is not an entry is
// nobody's to delete.
func TestTrimGoBuildCacheOnlyEvictsCacheEntries(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 8; i++ {
		addGoCacheEntry(t, dir, i, 200000, 300*time.Hour)
	}
	for name, body := range map[string]string{"trim.txt": "200\n", "README": "docs\n", "log.txt": "history\n", "00/not-an-entry": "stray\n"} {
		writeAssetFile(t, filepath.Join(dir, name), body)
	}
	ctx, _ := runtimeTestContext(false)
	if _, err := TrimGoBuildCache(ctx, dir, 100000); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"trim.txt", "README", "log.txt", "00/not-an-entry"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("the trim removed %s, which is not a cache entry", name)
		}
	}
}

// An action whose output has to be run is stored as a directory; it is sized by
// what it holds and goes with its contents.
func TestTrimGoBuildCacheEvictsAnExecutableEntryWhole(t *testing.T) {
	dir := t.TempDir()
	hash := fmt.Sprintf("%064x", 7)
	exe := filepath.Join(dir, "aa", hash+"-d")
	writeAssetFile(t, filepath.Join(exe, "exe"), strings.Repeat("x", 400000))
	writeAssetFile(t, filepath.Join(dir, "aa", hash+"-a"), "v1\n")
	old := time.Now().Add(-300 * time.Hour)
	for _, path := range []string{exe, filepath.Join(dir, "aa", hash+"-a")} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		addGoCacheEntry(t, dir, 900+i, 50000, time.Hour)
	}
	ctx, _ := runtimeTestContext(false)
	if _, err := TrimGoBuildCache(ctx, dir, 200000); err != nil {
		t.Fatal(err)
	}
	if size := mustGoCacheSize(t, dir); size > 200000 {
		t.Fatalf("an over-cap cache of executable entries should come within its cap, got %d", size)
	}
	if _, err := os.Stat(exe); !os.IsNotExist(err) {
		t.Fatalf("an evicted executable entry should take its directory with it: %v", err)
	}
}

func TestTrimGoBuildCacheDryRunRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 16; i++ {
		addGoCacheEntry(t, dir, i, 200000, 300*time.Hour)
	}
	full := mustGoCacheSize(t, dir)
	ctx, out := runtimeTestContext(true)
	result, err := TrimGoBuildCache(ctx, dir, full/4)
	if err != nil {
		t.Fatal(err)
	}
	if mustGoCacheSize(t, dir) != full {
		t.Fatal("--dry-run removed something")
	}
	if result.Evicted == 0 || !strings.Contains(out.String(), "nothing removed") {
		t.Fatalf("--dry-run should count what it would evict and say it removed nothing, got %+v %q", result, out.String())
	}
}

func TestTrimGoBuildCacheRefusesBadArguments(t *testing.T) {
	ctx, _ := runtimeTestContext(false)
	for _, tc := range []struct {
		dir      string
		maxBytes int64
	}{{"", 1}, {t.TempDir(), 0}, {t.TempDir(), -5}} {
		if _, err := TrimGoBuildCache(ctx, tc.dir, tc.maxBytes); err == nil {
			t.Fatalf("TrimGoBuildCache(%q, %d) should refuse its arguments", tc.dir, tc.maxBytes)
		}
	}
}

// Growth must not accumulate: each round adds as much as the cap holds, so a
// trim that removed the newest entries, or nothing, would end a round over it.
func TestTrimGoBuildCacheStaysBoundedUnderGrowth(t *testing.T) {
	dir := t.TempDir()
	const limit = 2000000
	ctx, _ := runtimeTestContext(false)
	for round := 0; round < 6; round++ {
		for i := 0; i < 24; i++ {
			addGoCacheEntry(t, dir, round*24+i, 400000, time.Duration(6-round)*time.Hour)
		}
		if _, err := TrimGoBuildCache(ctx, dir, limit); err != nil {
			t.Fatal(err)
		}
		if size := mustGoCacheSize(t, dir); size > limit {
			t.Fatalf("round %d: the cache grew past its %d byte cap to %d", round, limit, size)
		}
	}
	entries, err := goBuildCacheEntries(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("a bounded cache should still hold entries, not be emptied: %d, %v", len(entries), err)
	}
}

// A bounded cache is still a warm one: against the real toolchain, the entries
// the last build used survive a trim that evicts the aged bulk, so the next
// build is still answered from the cache.
func TestTrimGoBuildCacheKeepsTheWorkingSetWarm(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the warm-cache case needs the go toolchain on PATH: %v", err)
	}
	root := t.TempDir()
	module := filepath.Join(root, "module")
	writeAssetFile(t, filepath.Join(module, "go.mod"), "module example.com/warm\n\ngo 1.21\n")
	for _, pkg := range []string{"alpha", "beta"} {
		writeAssetFile(t, filepath.Join(module, pkg, pkg+".go"), "package "+pkg+"\n\nfunc Value() int { return 42 }\n")
		writeAssetFile(t, filepath.Join(module, pkg, pkg+"_test.go"), "package "+pkg+"\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 42 {\n\t\tt.Fatal(\"unexpected\")\n\t}\n}\n")
	}
	cache := filepath.Join(root, "go-cache")
	runTests := func() string {
		cmd := exec.Command(goBin, "test", "./...")
		cmd.Dir = module
		cmd.Env = append(os.Environ(), "GOCACHE="+cache, "GOPATH="+filepath.Join(root, "gopath"), "GOFLAGS=-mod=mod", "GO111MODULE=on")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go test: %v\n%s", err, out)
		}
		return string(out)
	}
	runTests()
	if out := runTests(); !strings.Contains(out, "(cached)") {
		t.Fatalf("an unchanged module should be served from the cache, got %s", out)
	}
	for i := 0; i < 40; i++ {
		addGoCacheEntry(t, cache, 5000+i, 300000, 200*time.Hour)
	}
	limit := mustGoCacheSize(t, cache) / 2
	ctx, _ := runtimeTestContext(false)
	if _, err := TrimGoBuildCache(ctx, cache, limit); err != nil {
		t.Fatal(err)
	}
	if size := mustGoCacheSize(t, cache); size > limit {
		t.Fatalf("the real cache should be within its %d byte cap, got %d", limit, size)
	}
	if out := runTests(); !strings.Contains(out, "(cached)") {
		t.Fatalf("bounding the cache must not make the next build cold, got %s", out)
	}
}
