package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// wailsjsHarness stages a miniature checkout -- an erun-ui and the sibling
// erun-common generate-wailsjs.sh derives both from its own location -- so the
// script's skip decision can be driven from either side of its cache key
// without a real wails CLI anywhere near it.
type wailsjsHarness struct {
	root     string
	uiDir    string
	common   string
	cacheDir string
	binDir   string
	goStub   string
	// omitWailsBin runs the script the way the Dockerfile does: WAILS_BIN
	// absent from the environment, so the script must fall back to the
	// toolchain-derived default.
	omitWailsBin bool
}

// wailsjsRun is one invocation: what it told the operator, and whether it
// regenerated (rather than reusing) the bindings.
type wailsjsRun struct {
	output      string
	generations int
	goCalls     string
}

func newWailsjsHarness(t *testing.T) *wailsjsHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("generate-wailsjs.sh and its stubs are POSIX shell")
	}
	// The declaration half of the key is read with `go doc`; a host without
	// the toolchain cannot exercise it, and saying so beats a confusing
	// failure from inside the script.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("generate-wailsjs.sh reads erun-common's declarations with the Go toolchain")
	}
	harness := &wailsjsHarness{
		root:     t.TempDir(),
		cacheDir: filepath.Join(t.TempDir(), "cache"),
		binDir:   t.TempDir(),
	}
	harness.uiDir = filepath.Join(harness.root, "erun-ui")
	harness.common = filepath.Join(harness.root, "erun-common")
	for _, dir := range []string{harness.uiDir, filepath.Join(harness.uiDir, "headlessserver"), harness.common} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("stage %s: %v", dir, err)
		}
	}
	// The script under test is the shipped one, read from this module, so the
	// harness cannot drift from it.
	harness.writeFile(t, "erun-ui/generate-wailsjs.sh", readBuildScript(t, "generate-wailsjs.sh"))
	harness.writeFile(t, "erun-ui/go.mod", "module erunui\n\ngo 1.21\n")
	harness.writeFile(t, "erun-ui/go.sum", "")
	harness.writeFile(t, "erun-ui/wails.json", "{\n  \"name\": \"ERun\"\n}\n")
	harness.writeFile(t, "erun-ui/main.go", "package main\n\nfunc Bound() string { return \"bound\" }\n")
	harness.writeFile(t, "erun-ui/headlessserver/server.go", "package headlessserver\n\nfunc Serve() {}\n")
	harness.writeFile(t, "erun-common/go.mod", "module eruncommon\n\ngo 1.21\n")
	harness.writeFile(t, "erun-common/go.sum", "")
	harness.setErunCommonPayload(t, "Name string")
	harness.writeWailsStub(t)
	return harness
}

func (h *wailsjsHarness) writeFile(t *testing.T, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.root, rel), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// setErunCommonPayload declares the type a bound erun-ui method could reach, so
// a test can change erun-common's declarations by exactly one field.
func (h *wailsjsHarness) setErunCommonPayload(t *testing.T, fields string) {
	t.Helper()
	h.writeFile(t, "erun-common/payload.go", "package eruncommon\n\ntype Payload struct {\n\t"+fields+"\n}\n")
}

func (h *wailsjsHarness) logPath(name string) string { return filepath.Join(h.root, name) }

// writeWailsStub stands in for `wails generate module`. It leaves a copy of the
// erun-common declarations it generated from, so a test can read the shipped
// bindings for staleness rather than only counting generations.
func (h *wailsjsHarness) writeWailsStub(t *testing.T) {
	t.Helper()
	stub := `#!/bin/sh
printf 'generate module\n' >> "$ERUN_TEST_WAILS_LOG"
mkdir -p frontend/wailsjs
cp ../erun-common/payload.go frontend/wailsjs/bindings.txt
`
	if err := os.WriteFile(filepath.Join(h.binDir, "wails"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write wails stub: %v", err)
	}
}

// stubGoInterpreter shadows the Go toolchain for the rest of the harness's
// runs. A cache hit must not need it at all -- and this one fails if called, so
// "did the hit path reach for the toolchain" becomes a decision the test can
// read, not a stopwatch it has to trust.
func (h *wailsjsHarness) stubGoInterpreter(t *testing.T) {
	t.Helper()
	h.goStub = t.TempDir()
	stub := "#!/bin/sh\nprintf 'go %s\\n' \"$*\" >> \"" + h.logPath("go-calls.log") + "\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(h.goStub, "go"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write go stub: %v", err)
	}
}

// writeGopathGoStub stands in for the host the Dockerfile builds on: no
// WAILS_BIN in the environment, a `go` that is present, and `wails` already
// installed at $(go env GOPATH)/bin/wails. It answers `env GOPATH` and fails
// every other subcommand, logging each call, so a run that finds its generator
// through the default is distinguishable from one that generates some other way.
func (h *wailsjsHarness) writeGopathGoStub(t *testing.T) {
	t.Helper()
	h.goStub = t.TempDir()
	gopath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gopath, "bin"), 0o755); err != nil {
		t.Fatalf("stage the installed wails: %v", err)
	}
	wailsBody, err := os.ReadFile(filepath.Join(h.binDir, "wails"))
	if err != nil {
		t.Fatalf("read the wails stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gopath, "bin", "wails"), wailsBody, 0o755); err != nil {
		t.Fatalf("install the wails stub: %v", err)
	}
	stub := "#!/bin/sh\n" +
		"printf 'go %s\\n' \"$*\" >> \"" + h.logPath("go-calls.log") + "\"\n" +
		"if [ \"$1\" = env ] && [ \"$2\" = GOPATH ]; then\n" +
		"\tprintf '%s\\n' \"" + gopath + "\"\n" +
		"\texit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(h.goStub, "go"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write go stub: %v", err)
	}
}

func (h *wailsjsHarness) run(t *testing.T) wailsjsRun {
	t.Helper()
	wailsLog, goLog := h.logPath("wails-calls.log"), h.logPath("go-calls.log")
	// Each run answers for itself: a log carrying the previous run's calls
	// would read as a regeneration this run never did.
	for _, log := range []string{wailsLog, goLog} {
		if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clear %s: %v", log, err)
		}
	}
	wailsBin := "WAILS_BIN=" + filepath.Join(h.binDir, "wails")
	if h.omitWailsBin {
		// Empty rather than unset: build.sh passes WAILS_BIN through from an
		// unset variable, and the script reads that as the default.
		wailsBin = "WAILS_BIN="
	}
	env := append(os.Environ(),
		wailsBin,
		"ERUN_WAILSJS_CACHE_DIR="+h.cacheDir,
		"ERUN_TEST_WAILS_LOG="+wailsLog,
	)
	if h.goStub != "" {
		env = append(env, "PATH="+h.goStub+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cmd := exec.Command("sh", "generate-wailsjs.sh")
	cmd.Dir = h.uiDir
	cmd.Env = env
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate-wailsjs.sh: %v (%s)", err, combined)
	}
	return wailsjsRun{
		output:      string(combined),
		generations: countLines(t, wailsLog),
		goCalls:     readFileIfPresent(t, goLog),
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	body := readFileIfPresent(t, path)
	if body == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(body, "\n"), "\n"))
}

func readFileIfPresent(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// bindings is what the generated directory currently holds -- the artifact the
// frontend actually type-checks and builds against.
func (h *wailsjsHarness) bindings(t *testing.T) string {
	t.Helper()
	return readFileIfPresent(t, filepath.Join(h.uiDir, "frontend", "wailsjs", "bindings.txt"))
}

// assertSkipped is the shape of a cache hit: no regeneration, no toolchain, and
// the operator told which it was rather than left to infer it from silence.
func (r wailsjsRun) assertSkipped(t *testing.T) {
	t.Helper()
	if r.generations != 0 {
		t.Errorf("bindings were regenerated %d time(s) with the bound Go API unchanged", r.generations)
	}
	if r.goCalls != "" {
		t.Errorf("a cache hit ran the Go toolchain (%s); the hit path must not pay a type-check", strings.TrimSpace(r.goCalls))
	}
	if !strings.Contains(r.output, "reusing cached bindings") {
		t.Errorf("a skipped generation did not say so: %s", r.output)
	}
}

func (r wailsjsRun) assertRegenerated(t *testing.T) {
	t.Helper()
	if r.generations != 1 {
		t.Fatalf("bindings were regenerated %d time(s), want exactly 1: %s", r.generations, r.output)
	}
	if !strings.Contains(r.output, "generated bindings") {
		t.Errorf("a generation did not report itself: %s", r.output)
	}
}

// The cache's first job: a second run that changed nothing must hand back the
// bindings the first one produced, and one change to the bound API must not.
func TestWailsjsCacheReusesBindingsUntilTheBoundGoAPIChanges(t *testing.T) {
	harness := newWailsjsHarness(t)

	harness.run(t).assertRegenerated(t)
	generated := harness.bindings(t)

	harness.run(t).assertSkipped(t)
	if got := harness.bindings(t); got != generated {
		t.Errorf("a reused cache shipped %q, want the bindings it was generated as, %q", got, generated)
	}

	// erun-ui's own bound package is the API itself.
	harness.writeFile(t, "erun-ui/main.go", "package main\n\nfunc Bound() int { return 1 }\n")
	harness.run(t).assertRegenerated(t)
}

// erun-common enters the key at declaration granularity, not file granularity:
// a change confined to a function body cannot alter what `wails generate
// module` emits, and erun-common is the module nearly every change touches, so
// keying it by file content would make a miss out of each one.
func TestWailsjsCacheSurvivesABodyOnlyChangeToErunCommon(t *testing.T) {
	harness := newWailsjsHarness(t)
	harness.writeFile(t, "erun-common/helper.go", "package eruncommon\n\nfunc Helper() int { return 1 }\n")
	harness.run(t).assertRegenerated(t)

	harness.writeFile(t, "erun-common/helper.go", "package eruncommon\n\nfunc Helper() int { return 2 }\n")
	harness.run(t).assertSkipped(t)
}

// The other half of that trade: a declaration erun-common actually exposes does
// reach the generated TS, so it must invalidate the cache and the bindings that
// ship afterwards must have been generated from it.
func TestWailsjsCacheMissesOnAnErunCommonDeclarationChange(t *testing.T) {
	harness := newWailsjsHarness(t)
	harness.run(t).assertRegenerated(t)

	harness.setErunCommonPayload(t, "Name string\n\tExtra int")
	harness.run(t).assertRegenerated(t)
	if got := harness.bindings(t); !strings.Contains(got, "Extra int") {
		t.Errorf("bindings regenerated but shipped as %q, which is the previous erun-common", got)
	}
}

// frontend/wailsjs is dockerignored, so the erun-devops image sees it absent on
// every build: a hit has to put the whole directory back, not merely decline to
// rerun the generator over one that was never there.
func TestWailsjsCacheRestoresTheBindingsADockerignoredCheckoutLacks(t *testing.T) {
	harness := newWailsjsHarness(t)
	harness.run(t).assertRegenerated(t)
	generated := harness.bindings(t)

	if err := os.RemoveAll(filepath.Join(harness.uiDir, "frontend", "wailsjs")); err != nil {
		t.Fatalf("remove the generated bindings: %v", err)
	}
	harness.run(t).assertSkipped(t)
	if got := harness.bindings(t); got != generated {
		t.Errorf("a reused cache restored %q, want %q", got, generated)
	}
}

// The reproduction of the stale-binding defect: when erun-common cannot be read
// as a package at all, the key must still be a function of what is on disk. A
// `go doc` that fails writes nothing to stdout, and hashing that empty output
// anyway collapses every unreadable revision onto one key -- so the next build
// reuses bindings generated from source that is gone, and reports success.
func TestWailsjsCacheDoesNotShipBindingsFromErunCommonItCouldNotRead(t *testing.T) {
	harness := newWailsjsHarness(t)
	// A syntax error leaves the package unreadable while the declarations a
	// bound method could reference change underneath it.
	harness.writeFile(t, "erun-common/broken.go", "package eruncommon\n\nfunc Broken( {\n")
	harness.run(t).assertRegenerated(t)

	harness.setErunCommonPayload(t, "Name string\n\tExtra int")
	harness.run(t).assertRegenerated(t)
	if got := harness.bindings(t); !strings.Contains(got, "Extra int") {
		t.Errorf("shipped bindings %q came from the erun-common that could no longer be read", got)
	}
}

// The reproduction of the intermittently-hitting cache: deciding a hit used to
// mean running `go doc`, which type-checks erun-common and its dependency graph
// and cannot answer without a resolvable module cache. So a hit cost seconds and
// the same unchanged tree could still miss, because the decision -- not the
// inputs -- was what varied. A hit must not reach for the toolchain at all.
func TestWailsjsCacheHitDoesNotInvokeTheGoToolchain(t *testing.T) {
	harness := newWailsjsHarness(t)
	harness.run(t).assertRegenerated(t)

	harness.stubGoInterpreter(t)
	harness.run(t).assertSkipped(t)
}

// The configuration above still exports WAILS_BIN. The gate never does: the
// Dockerfile sets no WAILS_BIN, and build.sh passes an unset one straight
// through, so the script finds its generator at $(go env GOPATH)/bin/wails --
// and resolving that default used to happen before the cache check. That put
// the toolchain on the hit path, so a `go env` that could not answer killed the
// script instead of either hitting or regenerating: a cache that dies on an
// unchanged tree is not a cache, and the same tree that hit a moment ago stops
// hitting for a reason nothing in the inputs changed.
func TestWailsjsCacheHitsWithoutAWailsBinInTheEnvironment(t *testing.T) {
	harness := newWailsjsHarness(t)
	harness.omitWailsBin = true
	harness.writeGopathGoStub(t)

	// The default is still how the generator is found: an absent WAILS_BIN
	// must not turn a miss into a silent no-op.
	harness.run(t).assertRegenerated(t)
	generated := harness.bindings(t)

	// The tree is untouched; only the toolchain stopped answering.
	harness.stubGoInterpreter(t)
	harness.run(t).assertSkipped(t)
	if got := harness.bindings(t); got != generated {
		t.Errorf("a reused cache shipped %q, want the bindings it was generated as, %q", got, generated)
	}
}
