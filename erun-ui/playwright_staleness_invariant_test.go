package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// stalenessInvariantRunScript is the suite's launcher. Its staleness scan is a
// correctness boundary in both directions: a tree compiled into
// ../bin/erun-app that the scan does not read lets a branch switch silently run
// the previous revision, and a tree the desktop does not compile from that the
// scan does read spends a desktop rebuild on unrelated work.
const stalenessInvariantRunScript = "playwright/run.sh"

// stalenessInvariantSettledAt and stalenessInvariantMovedAt are the fixture's
// two clocks, far enough apart that no filesystem's timestamp granularity can
// make "newer than the binary" come back false for a path a case moved.
var (
	stalenessInvariantSettledAt = time.Unix(1_700_000_000, 0)
	stalenessInvariantMovedAt   = time.Unix(1_700_000_100, 0)
)

// stalenessInvariantExcludedTree is a sibling module of erun-ui that the
// desktop neither links nor bundles: erun-console builds its own bundle and
// serves it from its own deploy artifacts, and no desktop build reads a byte of
// it. It is the tree the scan is most likely to widen onto — it is a workspace
// sibling, exactly like the erun-kit the scan is meant to read — so its
// exclusion is asserted rather than left to the accident of which roots the
// scan happens to be rooted at.
var stalenessInvariantExcludedTree = []string{
	"erun-console/package.json",
	"erun-console/src/App.tsx",
	"erun-console/src/main.tsx",
}

// stalenessInvariantScannedControl is a path every shape of this scan reads: it
// is a Go source of the module the binary is built from, so both a hand-list of
// packages and a whole-tree walk rooted at erun-ui report it.
const stalenessInvariantScannedControl = "erun-ui/main.go"

// stalenessInvariantHarness stages a miniature checkout shaped like the real
// one around run.sh's scan, and runs the scan itself against it.
type stalenessInvariantHarness struct {
	root       string
	erunUIDir  string
	binPath    string
	scanScript string
}

// stalenessInvariantScanFunction extracts find_stale_binary_sources from run.sh
// verbatim, so these cases drive the shipped predicate rather than a
// restatement of it that could agree with the test while the script disagreed
// with both.
func stalenessInvariantScanFunction(t *testing.T, script string) string {
	t.Helper()
	lines := strings.Split(script, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "find_stale_binary_sources() {" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s no longer defines find_stale_binary_sources(); the staleness scan was renamed or removed, and this test guards nothing until it is retargeted", stalenessInvariantRunScript)
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	t.Fatalf("%s's find_stale_binary_sources is not closed by a line holding only '}'", stalenessInvariantRunScript)
	return ""
}

func newStalenessInvariantHarness(t *testing.T) *stalenessInvariantHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("run.sh's staleness scan is POSIX shell (sh, find); the desktop is only cross-compiled on Windows")
	}
	root := t.TempDir()
	harness := &stalenessInvariantHarness{
		root:      root,
		erunUIDir: filepath.Join(root, "erun-ui"),
	}
	harness.binPath = filepath.Join(harness.erunUIDir, "bin", "erun-app")

	function := stalenessInvariantScanFunction(t, readBuildScript(t, stalenessInvariantRunScript))
	for _, path := range []string{root, harness.erunUIDir, harness.binPath} {
		if strings.Contains(path, "'") {
			t.Fatalf("fixture path %q carries a single quote, which the generated scan script cannot quote", path)
		}
	}
	harness.scanScript = "ERUN_UI_DIR='" + harness.erunUIDir + "'\n" +
		"BIN_PATH='" + harness.binPath + "'\n" +
		function + "\n" +
		"find_stale_binary_sources\n"
	return harness
}

// stage writes the binary the scan is deciding about, the path a control run
// moves, and the tree no case may see reported — all at the binary's own
// timestamp, so a case that moves one of them is the only reason the scan can
// report anything.
func (h *stalenessInvariantHarness) stage(t *testing.T) {
	t.Helper()
	files := []string{
		h.binPath,
		filepath.Join(h.root, filepath.FromSlash(stalenessInvariantScannedControl)),
	}
	for _, path := range stalenessInvariantExcludedTree {
		files = append(files, filepath.Join(h.root, filepath.FromSlash(path)))
	}
	for _, path := range files {
		h.touch(t, path, stalenessInvariantSettledAt)
	}
}

// touch writes path (creating parents) and pins its mtime, so "newer than the
// binary" is a property of this test rather than of how fast it ran.
func (h *stalenessInvariantHarness) touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("// fixture\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("set mtime on %s: %v", path, err)
	}
}

// move makes a staged path newer than the binary, which is what a branch switch
// or an edit under that path does on a real checkout.
func (h *stalenessInvariantHarness) move(t *testing.T, path string) {
	t.Helper()
	if err := os.Chtimes(path, stalenessInvariantMovedAt, stalenessInvariantMovedAt); err != nil {
		t.Fatalf("move %s ahead of the binary: %v", path, err)
	}
}

// scan runs the extracted predicate and returns the fixture-relative paths it
// reported.
func (h *stalenessInvariantHarness) scan(t *testing.T) []string {
	t.Helper()
	scriptPath := filepath.Join(h.root, "run-scan.sh")
	if err := os.WriteFile(scriptPath, []byte(h.scanScript), 0o755); err != nil {
		t.Fatalf("write scan script: %v", err)
	}
	cmd := exec.Command("sh", scriptPath)
	eruncommon.HideConsoleWindow(cmd)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run the extracted staleness scan: %v\n%s", err, combined)
	}
	var reported []string
	for _, line := range strings.Split(strings.TrimSpace(string(combined)), "\n") {
		if line == "" {
			continue
		}
		relative, relErr := filepath.Rel(h.root, line)
		if relErr != nil {
			relative = line
		}
		reported = append(reported, filepath.ToSlash(relative))
	}
	return reported
}

// TestPlaywrightStalenessScanLeavesErunConsoleOut pins the exclusion the
// coverage contract's other half rests on. The scan's coverage set is bounded
// by "compiled into bin/erun-app", not "present in the repository", and
// erun-console is the tree that bound is easiest to overshoot: it is a sibling
// workspace module whose package.json sits beside erun-kit's, so a scan widened
// to "the workspace siblings the suite's modules have" sweeps it in and every
// caller then pays a desktop rebuild for a change no desktop build reads.
//
// The second half is what keeps the first from being the harness's silence: the
// same fixture, with one path the binary IS built from moved as well, reports
// that path and nothing else, so a scan that had widened onto erun-console
// fails there naming the paths it swept in.
func TestPlaywrightStalenessScanLeavesErunConsoleOut(t *testing.T) {
	excluded := newStalenessInvariantHarness(t)
	excluded.stage(t)
	for _, path := range stalenessInvariantExcludedTree {
		excluded.move(t, filepath.Join(excluded.root, filepath.FromSlash(path)))
	}

	if reported := excluded.scan(t); len(reported) != 0 {
		t.Fatalf("the staleness scan reported %v for changes confined to erun-console, which the desktop neither links nor bundles; reaching into it spends a desktop rebuild on unrelated work", reported)
	}

	control := newStalenessInvariantHarness(t)
	control.stage(t)
	for _, path := range stalenessInvariantExcludedTree {
		control.move(t, filepath.Join(control.root, filepath.FromSlash(path)))
	}
	control.move(t, filepath.Join(control.root, filepath.FromSlash(stalenessInvariantScannedControl)))

	reported := control.scan(t)
	if len(reported) != 1 || reported[0] != stalenessInvariantScannedControl {
		t.Fatalf("the staleness scan reported %v; a tree the binary is compiled from must be reported, and erun-console, which it is not compiled from, must not be", reported)
	}
}
