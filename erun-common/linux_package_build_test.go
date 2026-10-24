package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
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

// Below debug verbosity a package build's BuildKit output is captured, so its
// test stage (erun-ui's runs for many minutes) is silent: the run's heartbeat is
// the only sign it is working rather than wedged, and it must name the package
// while its docker build runs. The stub holds the build until the test has
// looked at the heartbeat.
func TestLinuxPackageCheckIsOnTheBuildHeartbeatWhileItRuns(t *testing.T) {
	projectRoot, uiDir, _ := linuxPackageProject(t)
	packages := shared.NewLinuxPackageSpecs(projectRoot, []shared.LinuxPackageContext{{Dir: uiDir, DockerfilePath: filepath.Join(uiDir, "Dockerfile")}})
	gate := t.TempDir()
	stub := writeExecutableScript(t, "touch '"+gate+"/started'\nwhile [ ! -e '"+gate+"/release' ]; do sleep 0.01; done\nexit 0")
	t.Setenv("ERUN_DOCKER_BIN", stub)
	var log syncBuffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	ticks := make(chan time.Time)
	heartbeat := &core.BuildHeartbeat{Now: time.Now, Emit: func(string) {}, Ticks: ticks}
	ctx.Progress = heartbeat
	done := make(chan error, 1)
	go func() { done <- build.RunLinuxPackageChecks(ctx, packages) }()
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
