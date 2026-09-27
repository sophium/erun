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

// coverageRunScript is the suite's launcher. Its staleness scan is the only
// thing between a branch switch and a suite that silently exercises the
// previous revision: run.sh reuses ../bin/erun-app whenever the scan reports
// nothing newer, so any compiled-in input the scan does not read is a false
// verdict every spec in the run then inherits.
const coverageRunScript = "playwright/run.sh"

// The fixture's two clocks, far enough apart that no filesystem's timestamp
// granularity can make "newer than the binary" come back false for a file a
// case deliberately moved.
var (
	coverageSettledAt = time.Unix(1_700_000_000, 0)
	coverageMovedAt   = time.Unix(1_700_000_100, 0)
)

// coverageScanHarness stages a miniature checkout shaped like the real one
// around run.sh's scan, and runs the scan itself against it.
type coverageScanHarness struct {
	root       string
	erunUIDir  string
	binPath    string
	scanScript string
}

// coverageScanFunction extracts find_stale_binary_sources from run.sh verbatim,
// so these cases drive the shipped predicate rather than a restatement of it
// that could agree with the test while the script disagreed with both.
func coverageScanFunction(t *testing.T, script string) string {
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
		t.Fatalf("%s no longer defines find_stale_binary_sources(); the staleness scan was renamed or removed, and these cases guard nothing until they are retargeted", coverageRunScript)
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	t.Fatalf("%s's find_stale_binary_sources is not closed by a line holding only '}'", coverageRunScript)
	return ""
}

func newCoverageScanHarness(t *testing.T) *coverageScanHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("run.sh's staleness scan is POSIX shell (sh, find); the desktop is only cross-compiled on Windows")
	}
	root := t.TempDir()
	harness := &coverageScanHarness{
		root:      root,
		erunUIDir: filepath.Join(root, "erun-ui"),
	}
	harness.binPath = filepath.Join(harness.erunUIDir, "bin", "erun-app")

	function := coverageScanFunction(t, readBuildScript(t, coverageRunScript))
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

// stage writes every file the scan is expected to read, and every file a case
// asserts it must not read, all at the binary's own timestamp — so a case that
// moves one of them is the only reason the scan can report anything, and a
// pruned tree that is not staged cannot pass its case for being absent.
func (h *coverageScanHarness) stage(t *testing.T) {
	t.Helper()
	files := []string{
		// The binary the scan is deciding about.
		h.binPath,

		// This module's own Go sources, including a package the tree did not
		// have when the scan's path list was written.
		filepath.Join(h.erunUIDir, "main.go"),
		filepath.Join(h.erunUIDir, "platformclient", "client.go"),
		filepath.Join(h.erunUIDir, "headlessserver", "server.go"),

		// The Windows resource objects the linker folds into a windows build.
		filepath.Join(h.erunUIDir, "rsrc_windows_amd64.syso"),
		filepath.Join(h.erunUIDir, "rsrc_windows_arm64.syso"),

		// The release version build.sh stamps into the binary.
		filepath.Join(h.root, "erun-devops", "VERSION"),

		// erun-common, unioned in through go.work, including the assets it
		// go:embeds.
		filepath.Join(h.root, "erun-common", "common.go"),
		filepath.Join(h.root, "erun-common", "assets", "shell", "prompt.sh"),

		// The frontend project `wails generate module` / `yarn build` consume.
		filepath.Join(h.erunUIDir, "frontend", "src", "main.tsx"),
		filepath.Join(h.erunUIDir, "frontend", "index.html"),
		filepath.Join(h.erunUIDir, "frontend", "vite.config.ts"),
		filepath.Join(h.erunUIDir, "frontend", "package.json"),
		filepath.Join(h.erunUIDir, "frontend", "tsconfig.json"),

		// Trees the desktop does not compile from, staged so a walk that
		// widened into them is visible as a rebuild on unrelated work.
		filepath.Join(h.erunUIDir, "playwright", "fixtures", "winstub", "main.go"),
		filepath.Join(h.erunUIDir, "playwright", "node_modules", "flatted", "golang", "pkg", "flatted", "flatted.go"),
		filepath.Join(h.erunUIDir, "frontend", "node_modules", "dep", "bindings.go"),
	}
	for _, path := range files {
		h.touch(t, path, coverageSettledAt)
	}
}

// touch writes path (creating parents) and pins its mtime, so "newer than the
// binary" is a property of this test rather than of how fast it ran.
func (h *coverageScanHarness) touch(t *testing.T, path string, at time.Time) {
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

// move makes one staged path newer than the binary, which is what a branch
// switch or an edit under that path does on a real checkout.
func (h *coverageScanHarness) move(t *testing.T, path string) {
	t.Helper()
	if err := os.Chtimes(path, coverageMovedAt, coverageMovedAt); err != nil {
		t.Fatalf("move %s ahead of the binary: %v", path, err)
	}
}

// scan runs the extracted predicate and returns the fixture-relative paths it
// reported.
func (h *coverageScanHarness) scan(t *testing.T) []string {
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

// coverageInputCases are the paths the binary is built from that the scan must
// report when they move. The first row is a control: it is covered by the
// pre-existing scan, so a fixture that broke the harness outright would fail
// here rather than silently pass every gap case below.
var coverageInputCases = []struct {
	name string
	path string
}{
	{"erun-ui go source (control)", "erun-ui/main.go"},
	{"a package erun-ui gained", "erun-ui/platformclient/client.go"},
	{"erun-ui windows resource object", "erun-ui/rsrc_windows_amd64.syso"},
	{"erun-ui windows resource object (arm64)", "erun-ui/rsrc_windows_arm64.syso"},
	{"the release version build.sh stamps in", "erun-devops/VERSION"},
	{"erun-common go source (control)", "erun-common/common.go"},
	{"frontend source (control)", "erun-ui/frontend/src/main.tsx"},
}

// TestPlaywrightStalenessScanCoversEveryInputTheBinaryIsBuiltFrom is the
// reproduction of the false verdict this scan exists to prevent. Each row is a
// tree that reaches the built binary — a new package compiled into it, a
// Windows resource object linked into it, the version stamped into it — and a
// scan that does not read the row leaves a stale binary in place, which every
// spec in the run then exercises under the current branch's name.
func TestPlaywrightStalenessScanCoversEveryInputTheBinaryIsBuiltFrom(t *testing.T) {
	for _, testCase := range coverageInputCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newCoverageScanHarness(t)
			harness.stage(t)
			harness.move(t, filepath.Join(harness.root, filepath.FromSlash(testCase.path)))

			reported := harness.scan(t)

			if !coverageReported(reported, testCase.path) {
				t.Fatalf("the staleness scan did not report %s, so a change confined to it reuses the previous binary; it reported %v", testCase.path, reported)
			}
		})
	}
}

// TestPlaywrightStalenessScanLeavesTreesTheBinaryIsNotBuiltFromAlone is the
// other half of the contract, and the reason the coverage case above is a
// whole-tree walk rather than a wider hand-list. The error direction has to
// stay "rebuild unnecessarily": a scan that reached into the suite's own
// project — whose fixtures are their own Go module, built by nothing this
// script builds — or into installed dependencies would spend a desktop rebuild
// on files no desktop build reads.
func TestPlaywrightStalenessScanLeavesTreesTheBinaryIsNotBuiltFromAlone(t *testing.T) {
	harness := newCoverageScanHarness(t)
	harness.stage(t)
	for _, path := range []string{
		"erun-ui/playwright/fixtures/winstub/main.go",
		"erun-ui/playwright/node_modules/flatted/golang/pkg/flatted/flatted.go",
		"erun-ui/frontend/node_modules/dep/bindings.go",
	} {
		harness.move(t, filepath.Join(harness.root, filepath.FromSlash(path)))
	}

	reported := harness.scan(t)

	if len(reported) != 0 {
		t.Fatalf("the staleness scan reported %v for changes confined to trees no desktop build reads, which costs every caller a rebuild on unrelated work", reported)
	}
}

// TestPlaywrightStalenessScanIsSilentWhenNothingMoved keeps the cases above
// honest: a scan that reported everything unconditionally would satisfy the
// coverage case while making the suite rebuild on every run.
func TestPlaywrightStalenessScanIsSilentWhenNothingMoved(t *testing.T) {
	harness := newCoverageScanHarness(t)
	harness.stage(t)

	reported := harness.scan(t)

	if len(reported) != 0 {
		t.Fatalf("the staleness scan reported %v against a tree where nothing is newer than the binary", reported)
	}
}

// TestPlaywrightStalenessScanFixtureSpellingsMatch keeps the coverage case from
// passing for the wrong reason: every path it names must exist in the fixture
// under the same relative spelling, or `find` would be reporting a missing
// directory rather than a newer file.
func TestPlaywrightStalenessScanFixtureSpellingsMatch(t *testing.T) {
	harness := newCoverageScanHarness(t)
	harness.stage(t)
	for _, testCase := range coverageInputCases {
		path := filepath.Join(harness.root, filepath.FromSlash(testCase.path))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the fixture does not carry %s, so its case asserts nothing about the scan: %v", testCase.path, err)
		}
	}
}

// TestPlaywrightStalenessScanCoversTheWindowsTargetsResourceObjects locks the
// correspondence the .syso row rests on: rsrc_windows_<arch>.syso is selected
// by the toolchain for a windows target and by nothing else, which is what
// makes it a compiled-in input rather than a build artifact. It asserts the
// predicate's premise only — the object is what a windows build links — and
// nothing downstream of it, because the desktop's icon is not observable from
// any spec this suite can run. It skips where the desktop is not built for
// Windows, the target those objects exist for.
func TestPlaywrightStalenessScanCoversTheWindowsTargetsResourceObjects(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("rsrc_windows_<arch>.syso exists only for a windows target; the desktop is cross-compiled for Windows, not built on this host")
	}
	cmd := exec.Command("go", "list", "-f", "{{range .SysoFiles}}{{.}}\n{{end}}", ".")
	eruncommon.HideConsoleWindow(cmd)
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list the module's windows/amd64 inputs: %v", err)
	}
	if !strings.Contains(string(output), "rsrc_windows_amd64.syso") {
		t.Fatalf("the windows/amd64 target compiles no rsrc_windows_amd64.syso, so the scan's %q clause covers nothing on this tree; it reported %q", "*.syso", strings.TrimSpace(string(output)))
	}
}

func coverageReported(reported []string, want string) bool {
	for _, path := range reported {
		if path == want {
			return true
		}
	}
	return false
}
