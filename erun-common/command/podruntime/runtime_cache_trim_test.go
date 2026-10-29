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
