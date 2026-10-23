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

// linuxPackageProject lays out a <tenant>-devops tree with one image under
// docker/ and two packages under linux/: one with tests, one without.
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

func linuxPackageNames(packages []LinuxPackageSpec) []string {
	names := make([]string, 0, len(packages))
	for _, pkg := range packages {
		names = append(names, pkg.Name)
	}
	return names
}

func TestLinuxPackageDiscoveryFindsDockerfilesUnderLinux(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	devopsDir := filepath.Join(projectRoot, "team-devops")
	linuxDir := filepath.Join(devopsDir, "linux")
	cases := []struct {
		name string
		cwd  string
		want []string
	}{
		{"project root", projectRoot, []string{"erun-cli", "erun-ui"}},
		{"devops module", devopsDir, []string{"erun-cli", "erun-ui"}},
		{"linux module", linuxDir, []string{"erun-cli", "erun-ui"}},
		{"one package", uiDir, []string{"erun-ui"}},
		{"an image component", filepath.Join(devopsDir, "docker", "api"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, tc.cwd)
			contexts, err := ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, boundaryTarget(projectRoot))
			if tc.want == nil {
				if !errors.Is(err, ErrLinuxPackageBuildNotFound) {
					t.Fatalf("expected no packages, got %+v, %v", contexts, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := linuxPackageNames(newLinuxPackageSpecs(projectRoot, contexts))
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("packages = %v, want %v", got, tc.want)
			}
		})
	}
}

// A package directory holds a Dockerfile, but it is never an image: a build
// from it resolves no docker build, and a push from it is refused by name.
func TestLinuxPackageDockerfileIsNeverAnImage(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, uiDir)

	if contexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, boundaryTarget(projectRoot)); !errors.Is(err, ErrDockerBuildContextNotFound) {
		t.Fatalf("a package directory resolved image contexts %+v, %v", contexts, err)
	}
	execution, err := ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.dockerBuilds) != 0 || !slices.Equal(linuxPackageNames(execution.linuxPackages), []string{"erun-ui"}) {
		t.Fatalf("expected only the erun-ui package, got images %+v and packages %+v", execution.dockerBuilds, execution.linuxPackages)
	}
	_, _, err = ResolveDockerPushSpec(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil || !strings.Contains(err.Error(), "is a linux package, not an image") {
		t.Fatalf("a push from a package directory must be refused by name, got %v", err)
	}
}

// From the project root a plain build tests the packages beside the images,
// each with the project root as its context.
func TestPlainBuildResolvesLinuxPackagesBesideImages(t *testing.T) {
	projectRoot, _, _ := linuxPackageProject(t)
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, projectRoot)
	execution, err := ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.dockerBuilds) != 1 || len(execution.linuxPackages) != 2 {
		t.Fatalf("expected one image and two packages, got %+v and %+v", execution.dockerBuilds, execution.linuxPackages)
	}
	for _, pkg := range execution.linuxPackages {
		if pkg.ContextDir != projectRoot {
			t.Fatalf("package %s builds from %s, want the project root %s", pkg.Name, pkg.ContextDir, projectRoot)
		}
	}
	if result := NewBuildResult(execution); !slices.Equal(result.Packages, []string{"erun-cli", "erun-ui"}) {
		t.Fatalf("build result packages = %v", result.Packages)
	}
}

func TestPlainBuildRefusesAPackageWithNothingToRun(t *testing.T) {
	projectRoot, _, cliDir := linuxPackageProject(t)
	writeTestDockerfile(t, cliDir, "FROM debian:bookworm AS dist\nRUN true\n")
	findProjectRoot, resolveBuildContext := boundaryResolvers(projectRoot, projectRoot)
	_, err := ResolveBuildExecution(Context{DryRun: true}, nil, findProjectRoot, resolveBuildContext, nil, boundaryTarget(projectRoot))
	if err == nil || !strings.Contains(err.Error(), "linux package erun-cli declares neither a `test` nor a `build` stage") {
		t.Fatalf("expected the package without a test or build stage to be refused, got %v", err)
	}
}

// fakeDockerRecorder installs a docker stub through the ERUN_DOCKER_BIN seam
// that logs every invocation and, for a local export, writes a .deb named
// after the platform's directory into the destination.
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

// A plain build runs each package's test stage (or its build stage when it has
// none) to a cache-only output and reports the outcome; it never tags, pushes
// or exports anything.
func TestLinuxPackageChecksBuildCacheOnlyAndReportOutcomes(t *testing.T) {
	projectRoot, uiDir, cliDir := linuxPackageProject(t)
	packages := newLinuxPackageSpecs(projectRoot, []LinuxPackageContext{
		{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")},
		{Dir: cliDir, DockerfilePath: filepath.Join(cliDir, "Dockerfile")},
	})
	dockerLog := fakeDockerRecorder(t, "0")
	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	if err := runLinuxPackageChecks(ctx, packages); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"build --progress=plain --target test --output type=cacheonly -f " + filepath.Join(uiDir, "Dockerfile") + " .",
		"build --progress=plain --target build --output type=cacheonly -f " + filepath.Join(cliDir, "Dockerfile") + " .",
	}
	if got := readLines(t, dockerLog); !slices.Equal(got, want) {
		t.Fatalf("docker calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, line := range []string{"erun-ui: test: passed", "erun-cli: test: none declared"} {
		if !strings.Contains(log.String(), line) {
			t.Fatalf("missing outcome %q in:\n%s", line, log.String())
		}
	}
}

func TestLinuxPackageCheckFailureReportsNoOutcome(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	packages := newLinuxPackageSpecs(projectRoot, []LinuxPackageContext{{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")}})
	fakeDockerRecorder(t, "1")
	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	err := runLinuxPackageChecks(ctx, packages)
	if err == nil || !strings.Contains(err.Error(), "linux package erun-ui") || strings.Contains(log.String(), "test:") {
		t.Fatalf("expected a named failure and no outcome, got %v and %q", err, log.String())
	}
}

func linuxPackageReleaseSpec(t *testing.T, version string) ReleaseSpec {
	t.Helper()
	projectRoot, uiDir, cliDir := linuxPackageProject(t)
	packages, err := discoverReleaseLinuxPackages(projectRoot, filepath.Join(projectRoot, "team-devops"))
	if err != nil {
		t.Fatal(err)
	}
	if got := linuxPackageNames(packages); !slices.Equal(got, []string{"erun-cli", "erun-ui"}) {
		t.Fatalf("release packages = %v (%s, %s)", got, uiDir, cliDir)
	}
	return ReleaseSpec{ProjectRoot: projectRoot, Version: version, LinuxPackages: packages, LinuxPackageDir: filepath.Join(t.TempDir(), "linux")}
}

func TestReleaseRefusesAPackageWithoutADistStage(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	writeTestDockerfile(t, uiDir, "FROM golang:1.26.0 AS build\nRUN true\n\nFROM build AS test\nRUN true\n")
	_, err := discoverReleaseLinuxPackages(projectRoot, filepath.Join(projectRoot, "team-devops"))
	if err == nil || !strings.Contains(err.Error(), "linux package erun-ui declares no `dist` stage") {
		t.Fatalf("expected a package without dist to be refused, got %v", err)
	}
}

// A release exports every package's dist stage for both release platforms, and
// only the .deb files at the root of each export are uploaded. The release is
// created, as a prerelease for a candidate version, when it does not exist.
func TestReleaseExportsDistAndCreatesTheGitHubRelease(t *testing.T) {
	spec := linuxPackageReleaseSpec(t, "1.4.2-rc.1")
	dockerLog := fakeDockerRecorder(t, "0")
	ghLog := fakeGitHubCLI(t, "1")
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	stale := filepath.Join(spec.LinuxPackageDir, "erun-ui", "linux", "amd64", "erun-desktop_1.4.1.deb")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := exportReleaseLinuxPackages(ctx, spec); err != nil {
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

func assertGitHubReleaseCreated(t *testing.T, ctx Context, spec ReleaseSpec, ghLog string) {
	t.Helper()
	if err := uploadReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	gh := readLines(t, ghLog)
	if len(gh) != 2 || gh[0] != "release view v1.4.2-rc.1" {
		t.Fatalf("expected a view then a create, got:\n%s", strings.Join(gh, "\n"))
	}
	files, err := releaseLinuxPackageFiles(spec)
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
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := exportReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := uploadReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	gh := readLines(t, ghLog)
	if len(gh) != 2 || !strings.HasPrefix(gh[1], "release upload v1.4.2 ") || !strings.HasSuffix(gh[1], " --clobber") || strings.Contains(gh[1], "--prerelease") {
		t.Fatalf("expected a view then an upload --clobber, got:\n%s", strings.Join(gh, "\n"))
	}
}

// A dist stage that exports no .deb at its root uploads nothing, and the error
// says the tag is already public rather than suggesting the release be undone.
func TestReleaseUploadRefusesAnExportWithoutPackages(t *testing.T) {
	spec := linuxPackageReleaseSpec(t, "1.4.2")
	ghLog := fakeGitHubCLI(t, "0")
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{}), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	err := uploadReleaseLinuxPackages(ctx, spec)
	if err == nil || !strings.Contains(err.Error(), "exported no .deb file") || !strings.Contains(err.Error(), "do not delete the tag") {
		t.Fatalf("expected a missing-package failure, got %v", err)
	}
	if gh := readLines(t, ghLog); len(gh) != 0 {
		t.Fatalf("GitHub must not be reached, got:\n%s", strings.Join(gh, "\n"))
	}
}

// The dry run traces every export and both GitHub release branches, and runs
// nothing.
func TestReleaseLinuxPackagesDryRunTracesWithoutRunning(t *testing.T) {
	spec := linuxPackageReleaseSpec(t, "1.4.2")
	dockerLog := fakeDockerRecorder(t, "0")
	ghLog := fakeGitHubCLI(t, "0")
	var trace bytes.Buffer
	ctx := Context{DryRun: true, Logger: NewLoggerWithWriters(VerbosityTrace, &trace, &trace)}
	if err := exportReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := uploadReleaseLinuxPackages(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if calls := append(readLines(t, dockerLog), readLines(t, ghLog)...); len(calls) != 0 {
		t.Fatalf("a dry run must not run anything, got:\n%s", strings.Join(calls, "\n"))
	}
	out := trace.String()
	for _, want := range []string{
		"--platform linux/amd64 --target dist --build-arg ERUN_VERSION=1.4.2 --output 'type=local,dest=" + filepath.Join(spec.LinuxPackageDir, "erun-cli", "linux", "amd64") + "'",
		"--platform linux/arm64 --target dist",
		"stage: upload-linux-packages",
		"gh release view v1.4.2",
		"gh release upload v1.4.2 '" + filepath.Join(spec.LinuxPackageDir, "erun-cli", "linux", "amd64", "*.deb") + "'",
		"--title 'Release 1.4.2' --generate-notes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in dry-run trace:\n%s", want, out)
		}
	}
}

// Below debug verbosity a package build's BuildKit output is captured, so its
// test stage (erun-ui's runs for many minutes) is silent: the run's heartbeat is
// the only sign it is working rather than wedged, and it must name the package
// while its docker build runs. The stub holds the build until the test has
// looked at the heartbeat.
func TestLinuxPackageCheckIsOnTheBuildHeartbeatWhileItRuns(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	packages := newLinuxPackageSpecs(projectRoot, []LinuxPackageContext{{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")}})
	gate := t.TempDir()
	stub := writeExecutableScript(t, "touch '"+gate+"/started'\nwhile [ ! -e '"+gate+"/release' ]; do sleep 0.01; done\nexit 0")
	t.Setenv("ERUN_DOCKER_BIN", stub)
	var log syncBuffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	ticks := make(chan time.Time)
	heartbeat := &buildHeartbeat{now: time.Now, emit: func(string) {}, ticks: ticks}
	ctx.progress = heartbeat
	done := make(chan error, 1)
	go func() { done <- runLinuxPackageChecks(ctx, packages) }()
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
	line := heartbeat.line()
	if err := os.WriteFile(filepath.Join(gate, "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "still building erun-ui, ") {
		t.Fatalf("while the package build runs the heartbeat should name it, got %q", line)
	}
	if after := heartbeat.line(); after != "" {
		t.Fatalf("a finished package build must leave the heartbeat, got %q", after)
	}
}

// Inside a runtime pod every image build nests its RUN containers under the
// environment's CPU cap; a package build (erun-ui's test stage, the release's
// dist cross-builds) must too, or it escapes the dind sidecar's limit.
func TestLinuxPackageBuildsNestUnderTheCPUCapInsideARuntimePod(t *testing.T) {
	t.Setenv("ERUN_TENANT", "team")
	t.Setenv("ERUN_ENVIRONMENT", "dev")
	parent := buildContainerCPUCapCgroupParent()
	if parent == "" {
		t.Fatal("an injected runtime pod should have a CPU cap cgroup parent")
	}
	projectRoot, uiDir, _ := linuxPackageProject(t)
	packages := newLinuxPackageSpecs(projectRoot, []LinuxPackageContext{{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")}})
	dockerLog := fakeDockerRecorder(t, "0")
	var log bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	if err := runLinuxPackageChecks(ctx, packages); err != nil {
		t.Fatal(err)
	}
	spec := linuxPackageReleaseSpec(t, "1.2.3")
	if err := exportReleaseLinuxPackages(ctx, spec); err != nil {
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
