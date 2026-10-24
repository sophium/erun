package build

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// No test in this binary may read or write the operator's real package-test
// record: one that has not pointed the seam at its own tree finds no record
// and records nothing.
// No test asks a real docker daemon for its platform either: the daemon is
// linux/arm64 unless a test names another one.
func init() {
	LinuxPackageTestRecordDir = func() (string, error) {
		return "", errors.New("linux package test records are off in this test binary unless a test points them at its own directory")
	}
	LinuxPackageDaemonPlatform = func() (string, error) { return "linux/arm64", nil }
}

func useLinuxPackageTestRecordDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "package-tests")
	previous := LinuxPackageTestRecordDir
	LinuxPackageTestRecordDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { LinuxPackageTestRecordDir = previous })
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
		if err := RecordLinuxPackageTestPass(pkg.Name, fingerprint, "linux/arm64"); err != nil {
			t.Fatal(err)
		}
	}
	dockerLog := fakeDockerRecorder(t, "0")
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := ExportReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if calls := readLines(t, dockerLog); len(calls) != 2*len(spec.LinuxPackages) {
		t.Fatalf("a release must build every package's dist for both platforms, got:\n%s", strings.Join(calls, "\n"))
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
		if err := RecordLinuxPackageTestPass("app", fingerprint, "linux/amd64"); err != nil {
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
	if err := RecordLinuxPackageTestPass("app", "old", "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(linuxPackageTestRecordMaxAge + time.Minute)
	if linuxPackageTestPassed("app", "old", "linux/amd64") {
		t.Fatal("an expired pass must not be reused")
	}
	if err := RecordLinuxPackageTestPass("app", "fresh", "linux/amd64"); err != nil {
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
