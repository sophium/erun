package build

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// Keep tests off the operator's package-test record and any real docker daemon
// (linux/arm64 unless a test names another).
func init() {
	build.LinuxPackageTestRecordDir = func() (string, error) {
		return "", errors.New("linux package test records are off in this test binary unless a test points them at its own directory")
	}
	build.LinuxPackageDaemonPlatform = func() (string, error) { return "linux/arm64", nil }
}

func useLinuxPackageDaemonPlatform(t *testing.T, platform func() (string, error)) {
	t.Helper()
	previous := build.LinuxPackageDaemonPlatform
	build.LinuxPackageDaemonPlatform = platform
	t.Cleanup(func() { build.LinuxPackageDaemonPlatform = previous })
}

func useLinuxPackageTestRecordDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "package-tests")
	previous := build.LinuxPackageTestRecordDir
	build.LinuxPackageTestRecordDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { build.LinuxPackageTestRecordDir = previous })
	return dir
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

func reusablePackageProject(t *testing.T) (string, model.LinuxPackageSpec) {
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
	packages := dockerspec.NewLinuxPackageSpecs(root, []dockerspec.LinuxPackageContext{{Dir: dir, DockerfilePath: filepath.Join(dir, "Dockerfile")}})
	return root, packages[0]
}

func reuseDecision(t *testing.T, pkg model.LinuxPackageSpec) model.LinuxPackageSpec {
	t.Helper()
	decided := build.ApplyLinuxPackageReuse([]model.LinuxPackageSpec{pkg})[0]
	if decided.Fingerprint == "" {
		t.Fatalf("the package was not fingerprinted: %q", decided.NoReuseReason)
	}
	return decided
}

func runChecksLogging(t *testing.T, packages []model.LinuxPackageSpec) (string, error) {
	t.Helper()
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	err := build.RunLinuxPackageChecks(ctx, packages)
	return log.String(), err
}

func TestLinuxPackageFailedCheckRecordsNoPass(t *testing.T) {
	dir := useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	fakeDockerRecorder(t, "1")
	if _, err := runChecksLogging(t, []model.LinuxPackageSpec{reuseDecision(t, pkg)}); err == nil {
		t.Fatal("expected the failed check to fail the build")
	}
	if _, err := os.Stat(filepath.Join(dir, "app.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed check must record nothing, got %v", err)
	}
	if reuseDecision(t, pkg).Reuse {
		t.Fatal("a failed check must not be reused")
	}
	fakeDockerRecorder(t, "0")
	if _, err := runChecksLogging(t, []model.LinuxPackageSpec{reuseDecision(t, pkg)}); err != nil {
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
	ctx := core.Context{DryRun: true, Logger: core.NewLoggerWithWriters(core.VerbosityTrace, &log, &log)}
	decided := reuseDecision(t, pkg)
	if err := build.RunLinuxPackageChecks(ctx, []model.LinuxPackageSpec{decided}); err != nil {
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

func TestLinuxPackageFingerprintTracksTheCheckStagesCopySources(t *testing.T) {
	cases := []struct {
		name  string
		rel   string
		moves bool
	}{
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
			if err := build.RecordLinuxPackageTestPass(before.Name, before.Fingerprint, before.Platform); err != nil {
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

func TestLinuxPackagePassIsPerPlatform(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	useLinuxPackageDaemonPlatform(t, func() (string, error) { return "linux/arm64", nil })
	decided := reuseDecision(t, pkg)
	if decided.Platform != "linux/arm64" {
		t.Fatalf("platform = %q", decided.Platform)
	}
	if err := build.RecordLinuxPackageTestPass(decided.Name, decided.Fingerprint, decided.Platform); err != nil {
		t.Fatal(err)
	}
	useLinuxPackageDaemonPlatform(t, func() (string, error) { return "linux/amd64", nil })
	if reuseDecision(t, pkg).Reuse {
		t.Fatal("an arm64 pass must not be reused on amd64")
	}
}

func TestLinuxPackagePassIsKeyedByTheDaemonsPlatform(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	previousArch := core.CurrentGOARCH
	t.Cleanup(func() { core.CurrentGOARCH = previousArch })
	core.CurrentGOARCH = func() string { return "arm64" }
	useLinuxPackageDaemonPlatform(t, build.ReadDockerDaemonPlatform)
	dockerLog := filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `echo "$*" >> '`+dockerLog+`'`+"\n"+`echo linux/amd64`))

	decided := build.ApplyLinuxPackageReuse([]model.LinuxPackageSpec{pkg, pkg})
	for _, got := range decided {
		if got.Platform != "linux/amd64" {
			t.Fatalf("an amd64 daemon on an arm64 host must key the pass as linux/amd64, got %q (%s)", got.Platform, got.NoReuseReason)
		}
	}
	calls := readLines(t, dockerLog)
	if len(calls) != 1 || calls[0] != "version --format {{.Server.Os}}/{{.Server.Arch}}" {
		t.Fatalf("the daemon must be asked for its platform once per build, got:\n%s", strings.Join(calls, "\n"))
	}
}

func TestLinuxPackageWithoutTheDaemonsPlatformNeitherReusesNorRecords(t *testing.T) {
	recordDir := useLinuxPackageTestRecordDir(t)
	_, pkg := reusablePackageProject(t)
	for name, script := range map[string]string{
		"unreachable": "echo 'Cannot connect to the Docker daemon' >&2\nexit 1",
		"not linux":   "echo windows/amd64",
		"silent":      "exit 0",
	} {
		t.Run(name, func(t *testing.T) {
			useLinuxPackageDaemonPlatform(t, build.ReadDockerDaemonPlatform)
			t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, script))
			decided := build.ApplyLinuxPackageReuse([]model.LinuxPackageSpec{pkg})[0]
			if decided.Reuse || decided.Platform != "" || !strings.Contains(decided.NoReuseReason, "the docker daemon's platform could not be read") {
				t.Fatalf("expected no reuse decision and a reason, got %+v (%q)", decided, decided.NoReuseReason)
			}
			dockerLog := fakeDockerRecorder(t, "0")
			out, err := runChecksLogging(t, []model.LinuxPackageSpec{decided})
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

func TestBuildExecutionReuseDecisionRespectsNoIncremental(t *testing.T) {
	useLinuxPackageTestRecordDir(t)
	root, pkg := reusablePackageProject(t)
	decided := reuseDecision(t, pkg)
	if err := build.RecordLinuxPackageTestPass(decided.Name, decided.Fingerprint, decided.Platform); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Dir(pkg.DockerfilePath)
	findProjectRoot, resolveBuildContext := boundaryResolvers(root, pkgDir)

	target := boundaryTarget(root)
	target.NoIncremental = false
	execution, err := ResolveBuildExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.LinuxPackages) != 1 || !execution.LinuxPackages[0].Reuse {
		t.Fatalf("a plain build must reuse the recorded pass, got %+v", execution.LinuxPackages)
	}

	target.NoIncremental = true
	execution, err = ResolveBuildExecution(core.Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.LinuxPackages) != 1 || execution.LinuxPackages[0].Reuse || execution.LinuxPackages[0].Fingerprint != "" {
		t.Fatalf("--no-incremental must take no reuse decision, got %+v", execution.LinuxPackages)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
