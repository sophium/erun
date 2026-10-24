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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

const (
	testedLinuxPackageDockerfile = "" +
		"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
		"RUN go build -o /out/erun-app ./...\n" +
		"\n" +
		"FROM build AS test\n" +
		"RUN go test ./...\n" +
		"\n" +
		"FROM test AS package\n" +
		"ARG ERUN_VERSION\n" +
		"RUN dpkg-deb --build /pkg /erun-desktop_${ERUN_VERSION}.deb\n" +
		"\n" +
		"FROM scratch AS dist\n" +
		"COPY --from=package /*.deb /\n"
	untestedLinuxPackageDockerfile = "" +
		"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
		"RUN go build -o /out/erun ./...\n" +
		"\n" +
		"FROM build AS dist\n" +
		"RUN dpkg-deb --build /pkg /erun.deb\n"
)

func linuxPackageProject(t *testing.T) (projectRoot, uiDir, cliDir string) {
	t.Helper()
	projectRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestDockerfile(t, filepath.Join(projectRoot, "team-devops", "docker", "api"),
		"FROM alpine:3.22 AS build\nRUN true\n\nFROM build AS test\nRUN true\n\nFROM alpine:3.22 AS runtime\nCOPY --from=test /etc/hostname /hostname\n")
	uiDir = filepath.Join(projectRoot, "team-devops", "linux", "erun-ui")
	writeTestDockerfile(t, uiDir, testedLinuxPackageDockerfile)
	cliDir = filepath.Join(projectRoot, "team-devops", "linux", "erun-cli")
	writeTestDockerfile(t, cliDir, untestedLinuxPackageDockerfile)
	// A leftover script-based package directory is not a package.
	if err := os.MkdirAll(filepath.Join(projectRoot, "team-devops", "linux", "legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "team-devops", "linux", "legacy", "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return projectRoot, uiDir, cliDir
}

func linuxPackageNames(packages []model.LinuxPackageSpec) []string {
	names := make([]string, 0, len(packages))
	for _, pkg := range packages {
		names = append(names, pkg.Name)
	}
	return names
}

// fakeDockerRecorder logs each docker call and writes a .deb for a local export.
func fakeDockerRecorder(t *testing.T, exitCode string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	stub := writeExecutableScript(t, strings.Join([]string{
		`echo "$*" >> '` + logPath + `'`,
		`for arg in "$@"; do`,
		`  case "$arg" in`,
		`    type=local,dest=*) dest="${arg#type=local,dest=}"; mkdir -p "$dest/usr/share"; : > "$dest/pkg_$(basename "$dest").deb"; : > "$dest/usr/share/cached.deb" ;;`,
		`  esac`,
		`done`,
		`exit ` + exitCode,
	}, "\n"))
	t.Setenv("ERUN_DOCKER_BIN", stub)
	return logPath
}

func fakeGitHubCLI(t *testing.T, viewExit string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "gh.log")
	stub := writeExecutableScript(t, strings.Join([]string{
		`echo "$*" >> '` + logPath + `'`,
		`if [ "$1 $2" = "release view" ]; then exit ` + viewExit + `; fi`,
		`exit 0`,
	}, "\n"))
	t.Setenv("ERUN_GH_BIN", stub)
	return logPath
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

func TestLinuxPackageCheckFailureReportsNoOutcome(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	packages := dockerspec.NewLinuxPackageSpecs(projectRoot, []dockerspec.LinuxPackageContext{{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")}})
	fakeDockerRecorder(t, "1")
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	err := RunLinuxPackageChecks(ctx, packages)
	if err == nil || !strings.Contains(err.Error(), "linux package erun-ui") || strings.Contains(log.String(), "test:") {
		t.Fatalf("expected a named failure and no outcome, got %v and %q", err, log.String())
	}
}

func linuxPackageReleaseSpec(t *testing.T, version string) model.ReleaseSpec {
	t.Helper()
	projectRoot, uiDir, cliDir := linuxPackageProject(t)
	packages, err := DiscoverReleaseLinuxPackages(projectRoot, filepath.Join(projectRoot, "team-devops"))
	if err != nil {
		t.Fatal(err)
	}
	if got := linuxPackageNames(packages); !slices.Equal(got, []string{"erun-cli", "erun-ui"}) {
		t.Fatalf("release packages = %v (%s, %s)", got, uiDir, cliDir)
	}
	return model.ReleaseSpec{ProjectRoot: projectRoot, Version: version, LinuxPackages: packages, LinuxPackageDir: filepath.Join(t.TempDir(), "linux")}
}

func TestReleaseExportsDistAndCreatesTheGitHubRelease(t *testing.T) {
	spec := linuxPackageReleaseSpec(t, "1.4.2-rc.1")
	dockerLog := fakeDockerRecorder(t, "0")
	ghLog := fakeGitHubCLI(t, "1")
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	stale := filepath.Join(spec.LinuxPackageDir, "erun-ui", "linux", "amd64", "erun-desktop_1.4.1.deb")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ExportReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	calls := readLines(t, dockerLog)
	if len(calls) != 4 {
		t.Fatalf("expected two packages x two platforms, got:\n%s", strings.Join(calls, "\n"))
	}
	uiDockerfile := spec.LinuxPackages[1].DockerfilePath
	wantUIArm := "build --progress=plain --platform linux/arm64 --target dist --build-arg ERUN_VERSION=1.4.2-rc.1 --output type=local,dest=" +
		filepath.Join(spec.LinuxPackageDir, "erun-ui", "linux", "arm64") + " -f " + uiDockerfile + " ."
	if !slices.Contains(calls, wantUIArm) {
		t.Fatalf("missing %q in:\n%s", wantUIArm, strings.Join(calls, "\n"))
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an earlier attempt's export must be cleared before exporting, got %v", err)
	}
	assertGitHubReleaseCreated(t, ctx, spec, ghLog)
}

func assertGitHubReleaseCreated(t *testing.T, ctx core.Context, spec model.ReleaseSpec, ghLog string) {
	t.Helper()
	if err := dockerspec.UploadReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	gh := readLines(t, ghLog)
	if len(gh) != 2 || gh[0] != "release view v1.4.2-rc.1" {
		t.Fatalf("expected a view then a create, got:\n%s", strings.Join(gh, "\n"))
	}
	files, err := dockerspec.ReleaseLinuxPackageFiles(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 || slices.ContainsFunc(files, func(f string) bool { return strings.Contains(f, "cached.deb") }) {
		t.Fatalf("expected only the four root .deb files, got %v", files)
	}
	wantCreate := "release create v1.4.2-rc.1 " + strings.Join(files, " ") + " --title Release 1.4.2-rc.1 --prerelease --generate-notes"
	if gh[1] != wantCreate {
		t.Fatalf("create call:\n%s\nwant:\n%s", gh[1], wantCreate)
	}
}

func TestReleaseUploadsToAnExistingGitHubRelease(t *testing.T) {
	spec := linuxPackageReleaseSpec(t, "1.4.2")
	fakeDockerRecorder(t, "0")
	ghLog := fakeGitHubCLI(t, "0")
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := ExportReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := dockerspec.UploadReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	gh := readLines(t, ghLog)
	if len(gh) != 2 || !strings.HasPrefix(gh[1], "release upload v1.4.2 ") || !strings.HasSuffix(gh[1], " --clobber") || strings.Contains(gh[1], "--prerelease") {
		t.Fatalf("expected a view then an upload --clobber, got:\n%s", strings.Join(gh, "\n"))
	}
}

func TestReleaseUploadRefusesAnExportWithoutPackages(t *testing.T) {
	spec := linuxPackageReleaseSpec(t, "1.4.2")
	ghLog := fakeGitHubCLI(t, "0")
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	err := dockerspec.UploadReleaseLinuxPackages(ctx, spec)
	if err == nil || !strings.Contains(err.Error(), "exported no .deb file") || !strings.Contains(err.Error(), "do not delete the tag") {
		t.Fatalf("expected a missing-package failure, got %v", err)
	}
	if gh := readLines(t, ghLog); len(gh) != 0 {
		t.Fatalf("GitHub must not be reached, got:\n%s", strings.Join(gh, "\n"))
	}
}

func TestLinuxPackageBuildsNestUnderTheCPUCapInsideARuntimePod(t *testing.T) {
	t.Setenv("ERUN_TENANT", "team")
	t.Setenv("ERUN_ENVIRONMENT", "dev")
	parent := core.BuildContainerCPUCapCgroupParent()
	if parent == "" {
		t.Fatal("an injected runtime pod should have a CPU cap cgroup parent")
	}
	projectRoot, uiDir, _ := linuxPackageProject(t)
	packages := dockerspec.NewLinuxPackageSpecs(projectRoot, []dockerspec.LinuxPackageContext{{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")}})
	dockerLog := fakeDockerRecorder(t, "0")
	var log bytes.Buffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	if err := RunLinuxPackageChecks(ctx, packages); err != nil {
		t.Fatal(err)
	}
	spec := linuxPackageReleaseSpec(t, "1.2.3")
	if err := ExportReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, dockerLog)
	if len(lines) != 1+2*len(spec.LinuxPackages) {
		t.Fatalf("unexpected docker calls:\n%s", strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if !strings.Contains(line, "--cgroup-parent "+parent+" -f ") {
			t.Fatalf("every package build should carry --cgroup-parent %s, got %q", parent, line)
		}
	}
}

// The stub holds the build until the test has looked at the heartbeat.
func TestLinuxPackageCheckIsOnTheBuildHeartbeatWhileItRuns(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	packages := dockerspec.NewLinuxPackageSpecs(projectRoot, []dockerspec.LinuxPackageContext{{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")}})
	gate := t.TempDir()
	stub := writeExecutableScript(t, "touch '"+gate+"/started'\nwhile [ ! -e '"+gate+"/release' ]; do sleep 0.01; done\nexit 0")
	t.Setenv("ERUN_DOCKER_BIN", stub)
	var log syncBuffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	ticks := make(chan time.Time)
	heartbeat := &core.BuildHeartbeat{Now: time.Now, Emit: func(string) {}, Ticks: ticks}
	ctx.Progress = heartbeat
	done := make(chan error, 1)
	go func() { done <- RunLinuxPackageChecks(ctx, packages) }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(gate, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the package build never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	line := heartbeat.Line()
	if err := os.WriteFile(filepath.Join(gate, "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "still building erun-ui, ") {
		t.Fatalf("while the package build runs the heartbeat should name it, got %q", line)
	}
	if after := heartbeat.Line(); after != "" {
		t.Fatalf("a finished package build must leave the heartbeat, got %q", after)
	}
}
