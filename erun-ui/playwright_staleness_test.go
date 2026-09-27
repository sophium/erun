package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// playwrightRunScript is the suite's launcher. Its staleness scan is the one
// thing standing between a branch switch and a suite that silently exercises
// the previous revision: `run.sh` reuses ../bin/erun-app whenever the scan
// reports nothing newer, so any tree compiled into that binary the scan does
// not read is a false verdict every spec in the suite then inherits.
const playwrightRunScript = "playwright/run.sh"

// scanSettle and scanMoved are the fixture's two clocks. They are far enough
// apart that no filesystem's timestamp granularity can make "newer than the
// binary" come back false for a file this test deliberately moved.
var (
	scanSettle = time.Unix(1_700_000_000, 0)
	scanMoved  = time.Unix(1_700_000_100, 0)
)

// staleScanHarness stages a miniature checkout shaped like the real one around
// run.sh's scan, and runs the scan itself against it.
type staleScanHarness struct {
	root       string
	erunUIDir  string
	binPath    string
	scanScript string
}

// staleScanFunction extracts find_stale_binary_sources from run.sh verbatim, so
// the test drives the shipped predicate rather than a restatement of it that
// could agree with the test while the script disagreed with both.
func staleScanFunction(t *testing.T, script string) string {
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
		t.Fatalf("%s no longer defines find_stale_binary_sources(); the staleness scan was renamed or removed, and this test guards nothing until it is retargeted", playwrightRunScript)
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	t.Fatalf("%s's find_stale_binary_sources is not closed by a line holding only '}'", playwrightRunScript)
	return ""
}

func newStaleScanHarness(t *testing.T) *staleScanHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("run.sh's staleness scan is POSIX shell (find, sh); the desktop is only cross-compiled on Windows")
	}
	root := t.TempDir()
	harness := &staleScanHarness{
		root:      root,
		erunUIDir: filepath.Join(root, "erun-ui"),
	}
	harness.binPath = filepath.Join(harness.erunUIDir, "bin", "erun-app")

	function := staleScanFunction(t, readBuildScript(t, playwrightRunScript))
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

// stage writes every file the scan is expected to read, all at the binary's own
// timestamp, so a case that moves one of them is the only reason the scan can
// report anything.
func (h *staleScanHarness) stage(t *testing.T) {
	t.Helper()
	files := []string{
		// The binary the scan is deciding about.
		h.binPath,

		// erun-ui's own Go sources.
		filepath.Join(h.erunUIDir, "main.go"),
		filepath.Join(h.erunUIDir, "assets_production.go"),
		filepath.Join(h.erunUIDir, "headlessserver", "server.go"),

		// The module graph those sources are compiled against.
		filepath.Join(h.erunUIDir, "go.mod"),
		filepath.Join(h.erunUIDir, "go.sum"),
		filepath.Join(h.erunUIDir, "go.work"),
		filepath.Join(h.erunUIDir, "go.work.sum"),
		filepath.Join(h.root, "erun-common", "go.mod"),
		filepath.Join(h.root, "erun-common", "go.sum"),

		// erun-common, unioned in through go.work, including the assets it
		// go:embeds.
		filepath.Join(h.root, "erun-common", "common.go"),
		filepath.Join(h.root, "erun-common", "assets", "shell", "prompt.sh"),

		// The frontend project `wails generate module` / `yarn build` consume.
		filepath.Join(h.erunUIDir, "frontend", "src", "main.tsx"),
		filepath.Join(h.erunUIDir, "frontend", "src", "styles", "index.css"),
		filepath.Join(h.erunUIDir, "frontend", "index.html"),
		filepath.Join(h.erunUIDir, "frontend", "vite.config.ts"),
		filepath.Join(h.erunUIDir, "frontend", "package.json"),
		filepath.Join(h.erunUIDir, "frontend", "tsconfig.json"),

		// erun-kit: the frontend's sibling workspace module. `@kit` aliases into
		// its src tree, `erun-kit/theme.css` resolves to its stylesheet, and
		// Tailwind's @source scans it — all three compile into frontend/dist.
		filepath.Join(h.root, "erun-kit", "src", "index.ts"),
		filepath.Join(h.root, "erun-kit", "src", "styles", "theme.css"),
		filepath.Join(h.root, "erun-kit", "package.json"),

		// The workspace manifests build.sh installs the frontend from.
		filepath.Join(h.root, "package.json"),
		filepath.Join(h.root, "yarn.lock"),

		// A sibling the desktop does not compile from, staged so a scan that
		// widened into "any sibling module" is visible as a rebuild on
		// unrelated work.
		filepath.Join(h.root, "erun-console", "package.json"),
		filepath.Join(h.root, "erun-console", "src", "App.tsx"),
		filepath.Join(h.root, "erun-console", "src", "main.tsx"),
	}
	for _, path := range files {
		h.touch(t, path, scanSettle)
	}
}

// touch writes path (creating parents) and pins its mtime, so "newer than the
// binary" is a property of this test rather than of how fast it ran.
func (h *staleScanHarness) touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	body := []byte("// fixture\n")
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("set mtime on %s: %v", path, err)
	}
}

// move makes one staged file newer than the binary, which is exactly what a
// branch switch or an edit under that path does on a real checkout.
func (h *staleScanHarness) move(t *testing.T, path string) {
	t.Helper()
	if err := os.Chtimes(path, scanMoved, scanMoved); err != nil {
		t.Fatalf("move %s ahead of the binary: %v", path, err)
	}
}

// scan runs the extracted predicate and returns the relative paths it reported.
func (h *staleScanHarness) scan(t *testing.T) []string {
	t.Helper()
	scriptPath := filepath.Join(h.root, "run-scan.sh")
	if err := os.WriteFile(scriptPath, []byte(h.scanScript), 0o755); err != nil {
		t.Fatalf("write scan script: %v", err)
	}
	cmd := exec.Command("sh", scriptPath)
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

// TestPlaywrightStalenessScanCoversEveryTreeTheBinaryIsBuiltFrom is the
// reproduction of the false verdict this scan exists to prevent: a branch whose
// only change is under erun-kit leaves ../bin/erun-app in place, and the suite
// then exercises the previous revision under the new branch's name.
func TestPlaywrightStalenessScanCoversEveryTreeTheBinaryIsBuiltFrom(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"erun-kit source", "erun-kit/src/index.ts"},
		{"erun-kit stylesheet", "erun-kit/src/styles/theme.css"},
		{"erun-kit manifest", "erun-kit/package.json"},
		{"workspace manifest", "package.json"},
		{"workspace lockfile", "yarn.lock"},
		{"erun-ui go source", "erun-ui/main.go"},
		{"erun-ui headlessserver", "erun-ui/headlessserver/server.go"},
		{"erun-ui module manifest", "erun-ui/go.mod"},
		{"erun-ui module sums", "erun-ui/go.sum"},
		{"workspace go.work", "erun-ui/go.work"},
		{"workspace go.work sums", "erun-ui/go.work.sum"},
		{"erun-common module manifest", "erun-common/go.mod"},
		{"erun-common module sums", "erun-common/go.sum"},
		{"erun-common go source", "erun-common/common.go"},
		{"erun-common embedded asset", "erun-common/assets/shell/prompt.sh"},
		{"frontend source", "erun-ui/frontend/src/main.tsx"},
		{"frontend stylesheet", "erun-ui/frontend/src/styles/index.css"},
		{"frontend index", "erun-ui/frontend/index.html"},
		{"frontend vite config", "erun-ui/frontend/vite.config.ts"},
		{"frontend manifest", "erun-ui/frontend/package.json"},
		{"frontend tsconfig", "erun-ui/frontend/tsconfig.json"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newStaleScanHarness(t)
			harness.stage(t)
			harness.move(t, filepath.Join(harness.root, filepath.FromSlash(testCase.path)))

			reported := harness.scan(t)

			if !containsPath(reported, testCase.path) {
				t.Fatalf("the staleness scan did not report %s, so a change confined to it reuses the previous binary; it reported %v", testCase.path, reported)
			}
		})
	}
}

// TestPlaywrightStalenessScanLeavesUnrelatedModulesAlone is the other half of
// the contract: the scan has to be narrow enough that ordinary work in a module
// the desktop does not compile from does not cost every caller a desktop
// rebuild. erun-console is its own bundle and its own workspace consumer.
func TestPlaywrightStalenessScanLeavesUnrelatedModulesAlone(t *testing.T) {
	harness := newStaleScanHarness(t)
	harness.stage(t)
	harness.move(t, filepath.Join(harness.root, "erun-console", "src", "App.tsx"))
	harness.move(t, filepath.Join(harness.root, "erun-console", "src", "main.tsx"))
	harness.move(t, filepath.Join(harness.root, "erun-console", "package.json"))

	reported := harness.scan(t)

	if len(reported) != 0 {
		t.Fatalf("the staleness scan reported %v for a change confined to erun-console, which the desktop does not compile from", reported)
	}
}

// TestPlaywrightStalenessScanIsSilentWhenNothingMoved keeps the two cases above
// honest: a scan that reported everything unconditionally would satisfy the
// coverage case while making the suite rebuild on every run.
func TestPlaywrightStalenessScanIsSilentWhenNothingMoved(t *testing.T) {
	harness := newStaleScanHarness(t)
	harness.stage(t)

	reported := harness.scan(t)

	if len(reported) != 0 {
		t.Fatalf("the staleness scan reported %v against a tree where nothing is newer than the binary", reported)
	}
}

func containsPath(reported []string, want string) bool {
	for _, path := range reported {
		if path == want {
			return true
		}
	}
	return false
}

// TestPlaywrightStalenessScanActuallySeesEachFind keeps the coverage table from
// passing for the wrong reason: every path it names must exist in the fixture
// under the same relative spelling, or `find` would be reporting a missing
// directory rather than a newer file.
func TestPlaywrightStalenessScanFixtureSpellingsMatch(t *testing.T) {
	harness := newStaleScanHarness(t)
	harness.stage(t)
	for _, path := range []string{
		"erun-kit/src/index.ts",
		"erun-kit/src/styles/theme.css",
		"erun-kit/package.json",
		"package.json",
		"yarn.lock",
		"erun-ui/main.go",
		"erun-ui/headlessserver/server.go",
		"erun-ui/go.mod",
		"erun-ui/go.sum",
		"erun-ui/go.work",
		"erun-ui/go.work.sum",
		"erun-common/go.mod",
		"erun-common/go.sum",
		"erun-common/common.go",
		"erun-common/assets/shell/prompt.sh",
		"erun-ui/frontend/src/main.tsx",
		"erun-ui/frontend/src/styles/index.css",
		"erun-ui/frontend/index.html",
		"erun-ui/frontend/vite.config.ts",
		"erun-ui/frontend/package.json",
		"erun-ui/frontend/tsconfig.json",
		"erun-console/src/App.tsx",
	} {
		absolute := filepath.Join(harness.root, filepath.FromSlash(path))
		if _, err := os.Stat(absolute); err != nil {
			t.Errorf("fixture does not stage %s: %v", path, err)
		}
	}
	if !strings.Contains(harness.scanScript, fmt.Sprintf("BIN_PATH='%s'", harness.binPath)) {
		t.Fatal("the generated scan script does not bind BIN_PATH to the fixture binary")
	}
}
