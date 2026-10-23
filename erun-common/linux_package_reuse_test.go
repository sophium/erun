package eruncommon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// No test in this binary may read or write the operator's real package-test
// record: one that has not pointed the seam at its own tree finds no record
// and records nothing.
// No test asks a real docker daemon for its platform either: the daemon is
// linux/arm64 unless a test names another one.
func init() {
	linuxPackageTestRecordDir = func() (string, error) {
		return "", errors.New("linux package test records are off in this test binary unless a test points them at its own directory")
	}
	linuxPackageDaemonPlatform = func() (string, error) { return "linux/arm64", nil }
}

func useLinuxPackageDaemonPlatform(t *testing.T, platform func() (string, error)) {
	t.Helper()
	previous := linuxPackageDaemonPlatform
	linuxPackageDaemonPlatform = platform
	t.Cleanup(func() { linuxPackageDaemonPlatform = previous })
}

func useLinuxPackageTestRecordDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "package-tests")
	previous := linuxPackageTestRecordDir
	linuxPackageTestRecordDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { linuxPackageTestRecordDir = previous })
	return dir
}

func useLinuxPackageTestRecordClock(t *testing.T, now time.Time) *time.Time {
	t.Helper()
	clock := now
	previous := linuxPackageTestRecordNow
	linuxPackageTestRecordNow = func() time.Time { return clock }
	t.Cleanup(func() { linuxPackageTestRecordNow = previous })
	return &clock
}

const reusableLinuxPackageDockerfile = "" +
	"FROM --platform=$BUILDPLATFORM node:22.14-bookworm AS node\n" +
	"\n" +
	"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
	"COPY --from=node /usr/local/bin/node /usr/local/bin/node\n" +
	"COPY app /src/app\n" +
	"RUN go build ./...\n" +
	"\n" +
	"FROM build AS test\n" +
	"COPY tests /src/tests\n" +
	"RUN go test ./...\n" +
	"\n" +
	"FROM test AS package\n" +
	"COPY LICENSE /src/LICENSE\n" +
	"COPY team-devops/VERSION /src/VERSION\n" +
	"ARG ERUN_VERSION\n" +
	"RUN dpkg-deb --build /pkg /deb/app_${ERUN_VERSION}.deb\n" +
	"\n" +
	"FROM scratch AS dist\n" +
	"COPY --from=package /deb/ /\n"

// reusablePackageProject lays out one tested package whose stages COPY from
// the context: app/ into build, tests/ into test, LICENSE and VERSION into
// package only.
func reusablePackageProject(t *testing.T) (string, LinuxPackageSpec) {
	t.Helper()
	root := t.TempDir()
	for rel, content := range map[string]string{
		"app/main.go":                        "package main\n",
		"app/dist/generated.js":              "built on the host\n",
		"tests/main_test.go":                 "package main\n",
		"LICENSE":                            "license\n",
		"team-devops/VERSION":                "1.0.0\n",
		".dockerignore":                      "/app/dist\n",
		"team-devops/linux/app/Dockerfile":   reusableLinuxPackageDockerfile,
		"team-devops/docker/api/Dockerfile":  "FROM alpine:3.22\n",
		"team-devops/linux/app/unrelated.md": "not copied\n",
	} {
		writeComponentTreeFile(t, root, rel, content)
	}
	dir := filepath.Join(root, "team-devops", "linux", "app")
	packages := newLinuxPackageSpecs(root, []LinuxPackageContext{{Dir: dir, DockerfilePath: filepath.Join(dir, "Dockerfile")}})
	return root, packages[0]
}

func reuseDecision(t *testing.T, pkg LinuxPackageSpec) LinuxPackageSpec {
	t.Helper()
	decided := applyLinuxPackageReuse([]LinuxPackageSpec{pkg})[0]
	if decided.Fingerprint == "" {
		t.Fatalf("the package was not fingerprinted: %q", decided.noReuseReason)
	}
	return decided
}

func runChecksLogging(t *testing.T, packages []LinuxPackageSpec) (string, error) {
	t.Helper()
	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	err := runLinuxPackageChecks(ctx, packages)
	return log.String(), err
}

// The reported defect: an unchanged package reran its whole test stage on the
// next plain build, because only BuildKit's layer cache (which GC evicts)
// remembered the pass. After a pass, the same inputs reuse it and docker is
// not run at all.
func TestLinuxPackageReusesARecordedPassWhenUnchanged(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	dockerLog := fakeDockerRecorder(t, "0")

	first := reuseDecision(t, pkg)
	if first.Reuse {
		t.Fatal("nothing is recorded yet, so the first build must run the test stage")
	}
	if _, err := runChecksLogging(t, []LinuxPackageSpec{first}); err != nil {
		t.Fatal(err)
	}
	second := reuseDecision(t, pkg)
	if !second.Reuse || second.Fingerprint != first.Fingerprint {
		t.Fatalf("an unchanged package must reuse its pass, got %+v after %+v", second, first)
	}
	out, err := runChecksLogging(t, []LinuxPackageSpec{second})
	if err != nil {
		t.Fatal(err)
	}
	if want := "app: test: reused (fingerprint fp-" + first.Fingerprint + ")"; !strings.Contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
	if calls := readLines(t, dockerLog); len(calls) != 1 {
		t.Fatalf("the reused package must not run docker again, got:\n%s", strings.Join(calls, "\n"))
	}
}

func TestLinuxPackageFailedCheckRecordsNoPass(t *testing.T) {
	dir := useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	fakeDockerRecorder(t, "1")
	if _, err := runChecksLogging(t, []LinuxPackageSpec{reuseDecision(t, pkg)}); err == nil {
		t.Fatal("expected the failed check to fail the build")
	}
	if _, err := os.Stat(filepath.Join(dir, "app.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed check must record nothing, got %v", err)
	}
	if reuseDecision(t, pkg).Reuse {
		t.Fatal("a failed check must not be reused")
	}
	fakeDockerRecorder(t, "0")
	if _, err := runChecksLogging(t, []LinuxPackageSpec{reuseDecision(t, pkg)}); err != nil {
		t.Fatal(err)
	}
	if !reuseDecision(t, pkg).Reuse {
		t.Fatal("a passing check must be recorded")
	}
}

func TestLinuxPackageDryRunNeitherRunsNorRecords(t *testing.T) {
	dir := useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	dockerLog := fakeDockerRecorder(t, "0")
	var log bytes.Buffer
	ctx := Context{DryRun: true, Logger: NewLoggerWithWriters(VerbosityTrace, &log, &log)}
	decided := reuseDecision(t, pkg)
	if err := runLinuxPackageChecks(ctx, []LinuxPackageSpec{decided}); err != nil {
		t.Fatal(err)
	}
	if calls := readLines(t, dockerLog); len(calls) != 0 {
		t.Fatalf("a dry run must not run docker, got %v", calls)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a dry run must record nothing, got %v", err)
	}
	if want := "app: running its test stage: no recorded pass for fingerprint fp-" + decided.Fingerprint; !strings.Contains(log.String(), want) {
		t.Fatalf("missing %q in:\n%s", want, log.String())
	}
}

// Any file a stage up to the check target COPYs moves the fingerprint; a file
// the context ignores, a file nothing copies, and the version (which only the
// package stage consumes) do not.
func TestLinuxPackageFingerprintTracksTheCheckStagesCopySources(t *testing.T) {
	cases := []struct {
		name  string
		rel   string
		moves bool
	}{
		{"source the build stage copies", "app/main.go", true},
		{"new file under a copied directory", "app/extra.go", true},
		{"source the test stage copies", "tests/main_test.go", true},
		{"the Dockerfile", "team-devops/linux/app/Dockerfile", true},
		{"a dockerignored file under a copied directory", "app/dist/generated.js", false},
		{"the VERSION only the package stage copies", "team-devops/VERSION", false},
		{"the LICENSE only the package stage copies", "LICENSE", false},
		{"a file no stage copies", "team-devops/linux/app/unrelated.md", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useLinuxPackageTestRecordDir(t)
			root, pkg := reusablePackageProject(t)
			before := reuseDecision(t, pkg)
			if err := recordLinuxPackageTestPass(before.Name, before.Fingerprint, before.Platform); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, filepath.FromSlash(tc.rel))
			data, _ := os.ReadFile(path)
			writeComponentTreeFile(t, root, tc.rel, string(data)+"\n# changed\n")
			after := reuseDecision(t, pkg)
			if moved := after.Fingerprint != before.Fingerprint; moved != tc.moves {
				t.Fatalf("changing %s moved the fingerprint = %v, want %v", tc.rel, moved, tc.moves)
			}
			if after.Reuse == tc.moves {
				t.Fatalf("changing %s: reuse = %v, want %v", tc.rel, after.Reuse, !tc.moves)
			}
		})
	}
}

// A pass is recorded per platform: one recorded on arm64 is not a pass on
// amd64.
func TestLinuxPackagePassIsPerPlatform(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	useLinuxPackageDaemonPlatform(t, func() (string, error) { return "linux/arm64", nil })
	decided := reuseDecision(t, pkg)
	if decided.Platform != "linux/arm64" {
		t.Fatalf("platform = %q", decided.Platform)
	}
	if err := recordLinuxPackageTestPass(decided.Name, decided.Fingerprint, decided.Platform); err != nil {
		t.Fatal(err)
	}
	useLinuxPackageDaemonPlatform(t, func() (string, error) { return "linux/amd64", nil })
	if reuseDecision(t, pkg).Reuse {
		t.Fatal("an arm64 pass must not be reused on amd64")
	}
}

// The platform is the one the docker daemon builds for, not the host's CPU: a
// plain check build passes no --platform, so a remote daemon of another
// architecture runs it as its own. The daemon is asked once per build.
func TestLinuxPackagePassIsKeyedByTheDaemonsPlatform(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	previousArch := currentGOARCH
	t.Cleanup(func() { currentGOARCH = previousArch })
	currentGOARCH = func() string { return "arm64" }
	useLinuxPackageDaemonPlatform(t, readDockerDaemonPlatform)
	dockerLog := filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `echo "$*" >> '`+dockerLog+`'`+"\n"+`echo linux/amd64`))

	decided := applyLinuxPackageReuse([]LinuxPackageSpec{pkg, pkg})
	for _, got := range decided {
		if got.Platform != "linux/amd64" {
			t.Fatalf("an amd64 daemon on an arm64 host must key the pass as linux/amd64, got %q (%s)", got.Platform, got.noReuseReason)
		}
	}
	calls := readLines(t, dockerLog)
	if len(calls) != 1 || calls[0] != "version --format {{.Server.Os}}/{{.Server.Arch}}" {
		t.Fatalf("the daemon must be asked for its platform once per build, got:\n%s", strings.Join(calls, "\n"))
	}
}

// A daemon that cannot say what it builds for reuses nothing and records
// nothing: a pass filed under a guessed platform could be reused on another.
func TestLinuxPackageWithoutTheDaemonsPlatformNeitherReusesNorRecords(t *testing.T) {
	recordDir := useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	for name, script := range map[string]string{
		"unreachable": "echo 'Cannot connect to the Docker daemon' >&2\nexit 1",
		"not linux":   "echo windows/amd64",
		"silent":      "exit 0",
	} {
		t.Run(name, func(t *testing.T) {
			useLinuxPackageDaemonPlatform(t, readDockerDaemonPlatform)
			t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, script))
			decided := applyLinuxPackageReuse([]LinuxPackageSpec{pkg})[0]
			if decided.Reuse || decided.Platform != "" || !strings.Contains(decided.noReuseReason, "the docker daemon's platform could not be read") {
				t.Fatalf("expected no reuse decision and a reason, got %+v (%q)", decided, decided.noReuseReason)
			}
			dockerLog := fakeDockerRecorder(t, "0")
			out, err := runChecksLogging(t, []LinuxPackageSpec{decided})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "app: running its test stage: the docker daemon's platform could not be read") || !strings.Contains(out, "app: test: passed") {
				t.Fatalf("the package must run and say why it cannot reuse:\n%s", out)
			}
			if calls := readLines(t, dockerLog); len(calls) != 1 {
				t.Fatalf("expected one check build, got:\n%s", strings.Join(calls, "\n"))
			}
			if _, err := os.Stat(recordDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("nothing may be recorded without the daemon's platform, stat = %v", err)
			}
		})
	}
}

// A package without a `test` stage has no tests: its check build runs its
// `build` stage and reports `none declared`, and so does every build that
// reuses that pass, rather than `reused`, which says an earlier run's tests
// passed.
func TestLinuxPackageWithoutATestStageReportsNoneDeclaredWhenReused(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	root := t.TempDir()
	writeComponentTreeFile(t, root, "app/main.go", "package main\n")
	writeComponentTreeFile(t, root, "team-devops/linux/app/Dockerfile", "FROM golang:1.26.0 AS build\nCOPY app /src/app\nRUN go build ./...\n\nFROM build AS package\nRUN true\n")
	dir := filepath.Join(root, "team-devops", "linux", "app")
	pkg := newLinuxPackageSpecs(root, []LinuxPackageContext{{Dir: dir, DockerfilePath: filepath.Join(dir, "Dockerfile")}})[0]
	dockerLog := fakeDockerRecorder(t, "0")

	for i := range 2 {
		out, err := runChecksLogging(t, []LinuxPackageSpec{reuseDecision(t, pkg)})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "app: test: none declared") || strings.Contains(out, "test: reused") {
			t.Fatalf("build %d: a package without a test stage must report `none declared`, got:\n%s", i+1, out)
		}
	}
	if calls := readLines(t, dockerLog); len(calls) != 1 {
		t.Fatalf("the second build must still reuse the build stage's pass, got:\n%s", strings.Join(calls, "\n"))
	}
}

// A release always builds its dist stage, whatever a plain build recorded.
func TestReleaseNeverReusesARecordedPass(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	spec := linuxPackageReleaseSpec(t, "1.4.2")
	for _, pkg := range spec.LinuxPackages {
		if pkg.Reuse || pkg.Fingerprint != "" {
			t.Fatalf("a release package must carry no reuse decision, got %+v", pkg)
		}
		target, err := linuxPackageCheckTarget(pkg)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := linuxPackageCheckFingerprint(pkg, target)
		if err != nil {
			t.Fatal(err)
		}
		if err := recordLinuxPackageTestPass(pkg.Name, fingerprint, "linux/arm64"); err != nil {
			t.Fatal(err)
		}
	}
	dockerLog := fakeDockerRecorder(t, "0")
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := exportReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if calls := readLines(t, dockerLog); len(calls) != 2*len(spec.LinuxPackages) {
		t.Fatalf("a release must build every package's dist for both platforms, got:\n%s", strings.Join(calls, "\n"))
	}
}

// A release resolves no plain-build packages, and --no-incremental takes no
// reuse decision, so neither can skip a check.
func TestBuildExecutionReuseDecisionRespectsNoIncremental(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	root, pkg := reusablePackageProject(t)
	decided := reuseDecision(t, pkg)
	if err := recordLinuxPackageTestPass(decided.Name, decided.Fingerprint, decided.Platform); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Dir(pkg.DockerfilePath)
	findProjectRoot, resolveBuildContext := boundaryResolvers(root, pkgDir)

	target := boundaryTarget(root)
	target.NoIncremental = false
	execution, err := ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.linuxPackages) != 1 || !execution.linuxPackages[0].Reuse {
		t.Fatalf("a plain build must reuse the recorded pass, got %+v", execution.linuxPackages)
	}

	target.NoIncremental = true
	execution, err = ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.linuxPackages) != 1 || execution.linuxPackages[0].Reuse || execution.linuxPackages[0].Fingerprint != "" {
		t.Fatalf("--no-incremental must take no reuse decision, got %+v", execution.linuxPackages)
	}
}

func readPassedFingerprints(t *testing.T, name string) []string {
	t.Helper()
	record, _, err := readLinuxPackageTestRecord(name)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints := make([]string, 0, len(record.Passed))
	for _, pass := range record.Passed {
		fingerprints = append(fingerprints, pass.Fingerprint)
	}
	return fingerprints
}

func TestLinuxPackageTestRecordKeepsTheMostRecentPasses(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := useLinuxPackageTestRecordClock(t, start)
	var want []string
	for i := range linuxPackageTestRecordsKept + 3 {
		*clock = start.Add(time.Duration(i) * time.Hour)
		fingerprint := "fp" + string(rune('a'+i))
		if err := recordLinuxPackageTestPass("app", fingerprint, "linux/amd64"); err != nil {
			t.Fatal(err)
		}
		want = append(want, fingerprint)
	}
	want = want[len(want)-linuxPackageTestRecordsKept:]
	if got := readPassedFingerprints(t, "app"); !slices.Equal(got, want) {
		t.Fatalf("passes = %v, want the %d most recent %v", got, linuxPackageTestRecordsKept, want)
	}
	if linuxPackageTestPassed("app", "fpa", "linux/amd64") || !linuxPackageTestPassed("app", "fpk", "linux/amd64") {
		t.Fatal("a pruned pass must not be reused and the latest must be")
	}
}

func TestLinuxPackageTestRecordExpiresOldPasses(t *testing.T) {
	dir := useLinuxPackageTestRecordDir(t)
	clock := useLinuxPackageTestRecordClock(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := recordLinuxPackageTestPass("app", "old", "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(linuxPackageTestRecordMaxAge + time.Minute)
	if linuxPackageTestPassed("app", "old", "linux/amd64") {
		t.Fatal("an expired pass must not be reused")
	}
	if err := recordLinuxPackageTestPass("app", "fresh", "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	if got := readPassedFingerprints(t, "app"); !slices.Equal(got, []string{"fresh"}) {
		t.Fatalf("expired passes must be pruned on write, got %v", got)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("expected one record file per package, got %v, %v", entries, err)
	}
}

func TestDockerfileStageCopySourcesFollowsTheTargetsStages(t *testing.T) {
	dockerfile := "" +
		"ARG BASE=golang:1.26.0\n" +
		"FROM alpine:3.22 AS tools\n" +
		"COPY tools /tools\n" +
		"FROM golang:1.26.0 AS deps\n" +
		"COPY go.mod /src/go.mod\n" +
		"FROM golang:1.26.0\n" +
		"COPY unnamed /unnamed\n" +
		"FROM deps AS build\n" +
		"COPY --from=tools /tools /tools\n" +
		"RUN --mount=type=bind,from=2,source=/unnamed,target=/u true\n" +
		"COPY src /src\n" +
		"FROM build AS test\n" +
		"COPY --from=golang:1.26.0 /usr/local/go /go\n" +
		"COPY tests /tests\n" +
		"FROM test AS package\n" +
		"COPY VERSION /VERSION\n"
	flatten := func(groups [][]string) []string {
		var out []string
		for _, group := range groups {
			out = append(out, group...)
		}
		slices.Sort(out)
		return out
	}
	if got, want := flatten(dockerfileStageCopySources(dockerfile, "test")), []string{"go.mod", "src", "tests", "tools", "unnamed"}; !slices.Equal(got, want) {
		t.Fatalf("test stage sources = %v, want %v", got, want)
	}
	if got, want := flatten(dockerfileStageCopySources(dockerfile, "deps")), []string{"go.mod"}; !slices.Equal(got, want) {
		t.Fatalf("deps stage sources = %v, want %v", got, want)
	}
	if got, want := flatten(dockerfileStageCopySources(dockerfile, "missing")), []string{"VERSION", "go.mod", "src", "tests", "tools", "unnamed"}; !slices.Equal(got, want) {
		t.Fatalf("an unknown target must fall back to every source, got %v", got)
	}
}
