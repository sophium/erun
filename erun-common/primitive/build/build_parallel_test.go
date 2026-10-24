package build

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// FROM edges are read off disk, so the graph under test must be real files.
func buildSpecFromDockerfile(t *testing.T, dir, tag, from string) shared.DockerBuildSpec {
	t.Helper()
	path := filepath.Join(dir, "Dockerfile."+strings.ReplaceAll(tag, "/", "_"))
	body := "FROM " + from + "\nRUN true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	return shared.DockerBuildSpec{Image: helm.DockerImageReference{Tag: tag}, DockerfilePath: path, ContextDir: dir}
}

func dependencyContext(stdout, stderr io.Writer) core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard), Stdout: stdout, Stderr: stderr}
}

func traceContext(trace io.Writer) core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(0, trace, trace)}
}

func resolveTestDependencies(t *testing.T, builds []shared.DockerBuildSpec) ([]shared.DockerBuildSpec, map[string][]string) {
	t.Helper()
	ordered := orderedDockerBuildSpecs(builds)
	dependencies, err := buildDependencies(ordered)
	if err != nil {
		t.Fatalf("resolve dependencies: %v", err)
	}
	return ordered, dependencies
}

func TestBuildDependenciesRecordOnlyTheFromEdges(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
		buildSpecFromDockerfile(t, dir, "erun-backend-api", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "erun-docs", "node:22"),
	}
	_, dependencies := resolveTestDependencies(t, builds)

	if got := dependencies["erun-devops"]; len(got) != 1 || got[0] != "erun-ubuntu" {
		t.Fatalf("erun-devops must wait for erun-ubuntu alone, got %v", got)
	}
	for _, independent := range []string{"erun-ubuntu", "erun-backend-api", "erun-docs"} {
		if got := dependencies[independent]; len(got) != 0 {
			t.Fatalf("%s FROMs no sibling, so it waits for nothing, got %v", independent, got)
		}
	}
}

func TestBuildDependenciesIgnoreASelfReference(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "solo", "solo"),
	}
	_, dependencies := resolveTestDependencies(t, builds)
	if got := dependencies["solo"]; len(got) != 0 {
		t.Fatalf("a self-FROM is not a dependency, got %v", got)
	}
}

func TestTraceBuildDependencyPlanSaysNothingWhenNothingMustWait(t *testing.T) {
	dir := t.TempDir()
	for name, builds := range map[string][]shared.DockerBuildSpec{
		"one image": {buildSpecFromDockerfile(t, dir, "solo", "debian:bookworm")},
	} {
		t.Run(name, func(t *testing.T) {
			_, dependencies := resolveTestDependencies(t, builds)
			var trace bytes.Buffer
			traceBuildDependencyPlan(traceContext(&trace), builds, dependencies)
			if trace.Len() != 0 {
				t.Fatalf("nothing waits, so there is no ordering to explain, got %q", trace.String())
			}
		})
	}
}

func TestBuildDependenciesReportAFromCycleInsteadOfHanging(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "alpha", "beta"),
		buildSpecFromDockerfile(t, dir, "beta", "alpha"),
		// Naming a bystander would send a reader looking for an edge that is not there.
		buildSpecFromDockerfile(t, dir, "bystander", "debian:bookworm"),
	}
	_, err := buildDependencies(builds)
	if err == nil {
		t.Fatal("expected a FROM cycle to be reported")
	}
	for _, want := range []string{"cycle", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error must name %q, got %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "bystander") {
		t.Fatalf("the error must name the cycle alone, got %v", err)
	}
}

// erun-backend-api is held open; erun-devops, which only FROMs erun-ubuntu, must start anyway.
func TestRunBuildDependenciesStartsAnImageAsSoonAsItsOwnBasesAreDone(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
		buildSpecFromDockerfile(t, dir, "erun-backend-api", "debian:bookworm"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	slowStarted := make(chan struct{})
	devopsStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSlow) }) }
	defer release()

	build := func(spec shared.DockerBuildSpec, _, _ io.Writer) error {
		switch strings.TrimSpace(spec.Image.Tag) {
		case "erun-backend-api":
			close(slowStarted)
			<-releaseSlow
		case "erun-devops":
			close(devopsStarted)
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- runBuildDependencies(dependencyContext(io.Discard, io.Discard), ordered, dependencies, build, len(builds))
	}()

	select {
	case <-slowStarted:
	case <-time.After(30 * time.Second):
		t.Fatal("erun-backend-api never started")
	}
	select {
	case <-devopsStarted:
	case <-time.After(30 * time.Second):
		release()
		t.Fatal("erun-devops waited for an image it does not FROM: it must start once erun-ubuntu is done, not once every image beside it is done")
	}

	release()
	if err := <-done; err != nil {
		t.Fatalf("run builds: %v", err)
	}
}

func TestRunBuildDependenciesNeverStartsAnImageBeforeItsBases(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
		buildSpecFromDockerfile(t, dir, "erun-backend-api", "debian:bookworm"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}

	baseStarted := make(chan struct{})
	otherDone := make(chan struct{})
	build := func(spec shared.DockerBuildSpec, _, _ io.Writer) error {
		tag := strings.TrimSpace(spec.Image.Tag)
		record(tag + ":start")
		switch tag {
		case "erun-ubuntu":
			close(baseStarted)
			<-otherDone
		case "erun-backend-api":
			<-baseStarted
			close(otherDone)
		}
		record(tag + ":end")
		return nil
	}

	if err := runBuildDependencies(dependencyContext(io.Discard, io.Discard), ordered, dependencies, build, len(builds)); err != nil {
		t.Fatalf("run builds: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for tag, bases := range dependencies {
		for _, base := range bases {
			baseEnd := eventIndex(events, base+":end")
			dependentStart := eventIndex(events, tag+":start")
			if baseEnd < 0 || dependentStart < 0 {
				t.Fatalf("%s never ran against %s: %v", tag, base, events)
			}
			if dependentStart < baseEnd {
				t.Fatalf("%s started before its base %s finished: %v", tag, base, events)
			}
		}
	}
}

func TestRunBuildDependenciesFlushesOutputInBuildOrderWhateverFinishesFirst(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "slow", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "fast", "debian:bookworm"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	// The first image finishes last, so flushing on completion would reverse them.
	var released sync.WaitGroup
	released.Add(1)
	build := func(spec shared.DockerBuildSpec, stdout, _ io.Writer) error {
		if strings.TrimSpace(spec.Image.Tag) == "fast" {
			_, _ = fmt.Fprintln(stdout, "building fast")
			released.Done()
			return nil
		}
		released.Wait()
		_, _ = fmt.Fprintln(stdout, "building slow")
		return nil
	}

	var out bytes.Buffer
	ctx := dependencyContext(&out, io.Discard)
	if err := runBuildDependencies(ctx, ordered, dependencies, build, len(builds)); err != nil {
		t.Fatalf("run builds: %v", err)
	}
	if got := out.String(); got != "building slow\nbuilding fast\n" {
		t.Fatalf("output must follow the build order, got %q", got)
	}
}

// erun-devops (third) finishes while erun-backend-api (second) runs; the stream keeps build order.
func TestRunBuildDependenciesPublishesInBuildOrderAcrossTheOldBarrier(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-backend-api", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)
	if got := strings.TrimSpace(ordered[2].Image.Tag); got != "erun-devops" {
		t.Fatalf("the fixture expects erun-devops last in build order, got %q", got)
	}

	devopsDone := make(chan struct{})
	build := func(spec shared.DockerBuildSpec, stdout, _ io.Writer) error {
		tag := strings.TrimSpace(spec.Image.Tag)
		_, _ = fmt.Fprintln(stdout, tag)
		if tag == "erun-devops" {
			close(devopsDone)
			return nil
		}
		if tag == "erun-backend-api" {
			<-devopsDone
		}
		return nil
	}

	var out bytes.Buffer
	ctx := dependencyContext(&out, io.Discard)
	if err := runBuildDependencies(ctx, ordered, dependencies, build, len(builds)); err != nil {
		t.Fatalf("run builds: %v", err)
	}
	want := "erun-ubuntu\nerun-backend-api\nerun-devops\n"
	if got := out.String(); got != want {
		t.Fatalf("output must follow the build order, not the finish order: got %q, want %q", got, want)
	}
}

func TestRunBuildDependenciesReportsTheFirstFailureInBuildOrder(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "first", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "second", "debian:bookworm"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	build := func(spec shared.DockerBuildSpec, _, _ io.Writer) error {
		return errors.New(strings.TrimSpace(spec.Image.Tag) + " failed")
	}
	ctx := dependencyContext(io.Discard, io.Discard)
	err := runBuildDependencies(ctx, ordered, dependencies, build, len(builds))
	if err == nil || !strings.Contains(err.Error(), "first failed") {
		t.Fatalf("expected the build-order-first failure, got %v", err)
	}
}

func TestRunBuildDependenciesSkipsDependentsOfAFailedBase(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	var dependentRan atomic.Bool
	build := func(spec shared.DockerBuildSpec, _, _ io.Writer) error {
		if strings.TrimSpace(spec.Image.Tag) == "erun-devops" {
			dependentRan.Store(true)
			return nil
		}
		return errors.New("erun-ubuntu failed")
	}
	ctx := dependencyContext(io.Discard, io.Discard)
	err := runBuildDependencies(ctx, ordered, dependencies, build, len(builds))
	if err == nil || !strings.Contains(err.Error(), "erun-ubuntu failed") {
		t.Fatalf("expected the base's failure, got %v", err)
	}
	if dependentRan.Load() {
		t.Fatal("erun-devops built on a base that failed")
	}
}

func TestRunBuildDependenciesNeverExceedsItsBound(t *testing.T) {
	dir := t.TempDir()
	builds := []shared.DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "base", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "dependent", "base"),
	}
	for i := range 6 {
		builds = append(builds, buildSpecFromDockerfile(t, dir, fmt.Sprintf("image-%d", i), "debian:bookworm"))
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	var running, peak atomic.Int64
	build := func(shared.DockerBuildSpec, io.Writer, io.Writer) error {
		current := running.Add(1)
		for {
			seen := peak.Load()
			if current <= seen || peak.CompareAndSwap(seen, current) {
				break
			}
		}
		running.Add(-1)
		return nil
	}
	ctx := dependencyContext(io.Discard, io.Discard)
	if err := runBuildDependencies(ctx, ordered, dependencies, build, 2); err != nil {
		t.Fatalf("run builds: %v", err)
	}
	if peak.Load() > 2 {
		t.Fatalf("ran %d builds at once, bound was 2", peak.Load())
	}
}

func eventIndex(events []string, want string) int {
	for i, event := range events {
		if event == want {
			return i
		}
	}
	return -1
}

func TestResolveBuildJobsPrefersTheExplicitDegreeThenTheEnvironment(t *testing.T) {
	t.Setenv(BuildJobsEnvVar, "4")
	if got := resolveBuildJobs(core.Context{BuildJobs: 1}, 8); got != 1 {
		t.Fatalf("an explicit --jobs 1 must win, got %d", got)
	}
	if got := resolveBuildJobs(core.Context{}, 8); got != 4 {
		t.Fatalf("the environment override must apply, got %d", got)
	}
	if got := resolveBuildJobs(core.Context{BuildJobs: 16}, 3); got != 3 {
		t.Fatalf("expected the degree capped at the image count, got %d", got)
	}
}
