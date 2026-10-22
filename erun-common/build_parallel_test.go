package eruncommon

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
)

// buildSpecFromDockerfile writes a Dockerfile and returns the spec that builds
// it. The FROM edges are read off disk, so the graph under test has to be real
// files rather than a hand-built adjacency map.
func buildSpecFromDockerfile(t *testing.T, dir, tag, from string) DockerBuildSpec {
	t.Helper()
	path := filepath.Join(dir, "Dockerfile."+strings.ReplaceAll(tag, "/", "_"))
	body := "FROM " + from + "\nRUN true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	return DockerBuildSpec{Image: DockerImageReference{Tag: tag}, DockerfilePath: path, ContextDir: dir}
}

func dependencyContext(stdout, stderr io.Writer) Context {
	return Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard), Stdout: stdout, Stderr: stderr}
}

// traceContext captures the audit channel, which is where the dependency plan
// is reported.
func traceContext(trace io.Writer) Context {
	return Context{Logger: NewLoggerWithWriters(0, trace, trace)}
}

func resolveTestDependencies(t *testing.T, builds []DockerBuildSpec) ([]DockerBuildSpec, map[string][]string) {
	t.Helper()
	ordered := orderedDockerBuildSpecs(builds)
	dependencies, err := buildDependencies(ordered)
	if err != nil {
		t.Fatalf("resolve dependencies: %v", err)
	}
	return ordered, dependencies
}

// The graph is the whole scheduling contract, so it has to record the FROM edges
// and nothing else. erun-devops FROMs erun-ubuntu alone; it must not be recorded
// as waiting on the images it merely happens to be discovered beside, however
// long those take.
func TestBuildDependenciesRecordOnlyTheFromEdges(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
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

// A Dockerfile that FROMs itself is not waiting for anything: an image cannot
// wait for its own tags, and recording the edge would strand it forever.
func TestBuildDependenciesIgnoreASelfReference(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "solo", "solo"),
	}
	_, dependencies := resolveTestDependencies(t, builds)
	if got := dependencies["solo"]; len(got) != 0 {
		t.Fatalf("a self-FROM is not a dependency, got %v", got)
	}
}

// The audit line names the dependency structure, not a schedule: it says what
// has to wait for what, which is what holds whatever the run does. A build with
// nothing to order says nothing — every independent-image golden would churn for
// no information.
func TestTraceBuildDependencyPlanSaysNothingWhenNothingMustWait(t *testing.T) {
	dir := t.TempDir()
	for name, builds := range map[string][]DockerBuildSpec{
		"one image": {buildSpecFromDockerfile(t, dir, "solo", "debian:bookworm")},
		"no edges": {
			buildSpecFromDockerfile(t, dir, "api", "debian:bookworm"),
			buildSpecFromDockerfile(t, dir, "console", "node:22"),
		},
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

func TestTraceBuildDependencyPlanNamesWhatWaitsForWhat(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
		buildSpecFromDockerfile(t, dir, "erun-backend-api", "debian:bookworm"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	var trace bytes.Buffer
	traceBuildDependencyPlan(traceContext(&trace), ordered, dependencies)
	want := "build: 3 images, 1 waiting on a sibling base — erun-devops after erun-ubuntu"
	if got := strings.TrimSpace(trace.String()); got != want {
		t.Fatalf("dependency plan = %q, want %q", got, want)
	}
	// The line must not describe the run as staged: an image starts when its own
	// bases are done, and erun-devops does not wait for erun-backend-api.
	for _, forbidden := range []string{"wave", "erun-backend-api"} {
		if strings.Contains(trace.String(), forbidden) {
			t.Fatalf("the plan must not claim %q: %q", forbidden, trace.String())
		}
	}
}

// The sequential loop tolerated a FROM cycle by simply picking an order. A
// dependency-driven scheduler would wait forever on bases that are waiting on
// it, so the cycle has to be found and reported rather than hung on.
func TestBuildDependenciesReportAFromCycleInsteadOfHanging(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "alpha", "beta"),
		buildSpecFromDockerfile(t, dir, "beta", "alpha"),
		// An image the cycle cannot be blamed for: it is in no cycle at all, and
		// naming it would send a reader looking for an edge that is not there.
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

// The reported behaviour: erun-devops FROMs erun-ubuntu, which is a tag write
// that finishes in under a second, and it used to wait out the whole dependency
// level behind it — here, erun-backend-api, which it does not depend on. This
// holds erun-backend-api open and asserts erun-devops starts anyway. On the
// barrier it could not: erun-devops was the next level, so it started only once
// every image beside its base had finished.
func TestRunBuildDependenciesStartsAnImageAsSoonAsItsOwnBasesAreDone(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
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
	// Whatever the assertions do, the blocked build is released, so a failure
	// here cannot leave the run's goroutines behind.
	defer release()

	build := func(spec DockerBuildSpec, _, _ io.Writer) error {
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
	// A bound, not a sleep: erun-devops must start on its own, and if it never
	// does the run is waiting on an edge the Dockerfiles do not declare.
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

// Dispatch decides when an image may start, not whether its bases held: an image
// whose Dockerfile FROMs a sibling still cannot start until that sibling has
// finished and written its tags, whatever else is running. The base is held open
// while the scheduler has other work in flight, so a scheduler that started a
// dependent early would record it here.
func TestRunBuildDependenciesNeverStartsAnImageBeforeItsBases(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
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
	build := func(spec DockerBuildSpec, _, _ io.Writer) error {
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

// Concurrency must not reach the reader: two runs of the same build produce the
// same stream, and each image's output stays in one piece rather than
// interleaving with its neighbours.
func TestRunBuildDependenciesFlushesOutputInBuildOrderWhateverFinishesFirst(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "slow", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "fast", "debian:bookworm"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	// The first image finishes last, so an implementation that flushed on
	// completion would emit them the other way round.
	var released sync.WaitGroup
	released.Add(1)
	build := func(spec DockerBuildSpec, stdout, _ io.Writer) error {
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

// The order independent dispatch has to be shown to keep: an image that ran
// ahead of another still publishes after it. The barrier got this for free — no
// later image existed yet when an earlier one was flushed — so it is the
// property most at risk when images start whenever their own bases are done.
//
// erun-devops (third in build order) finishes while erun-backend-api (second) is
// still running; the stream must still read backend-api before devops.
func TestRunBuildDependenciesPublishesInBuildOrderAcrossTheOldBarrier(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-backend-api", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)
	if got := strings.TrimSpace(ordered[2].Image.Tag); got != "erun-devops" {
		t.Fatalf("the fixture expects erun-devops last in build order, got %q", got)
	}

	devopsDone := make(chan struct{})
	build := func(spec DockerBuildSpec, stdout, _ io.Writer) error {
		tag := strings.TrimSpace(spec.Image.Tag)
		_, _ = fmt.Fprintln(stdout, tag)
		if tag == "erun-devops" {
			close(devopsDone)
			return nil
		}
		if tag == "erun-backend-api" {
			// Runs ahead of its own build order: it is still going when the
			// image after it has already finished.
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

// Failures are reported in build order too, so which error a build reports does
// not depend on which goroutine lost the race.
func TestRunBuildDependenciesReportsTheFirstFailureInBuildOrder(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "first", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "second", "debian:bookworm"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	build := func(spec DockerBuildSpec, _, _ io.Writer) error {
		return errors.New(strings.TrimSpace(spec.Image.Tag) + " failed")
	}
	ctx := dependencyContext(io.Discard, io.Discard)
	err := runBuildDependencies(ctx, ordered, dependencies, build, len(builds))
	if err == nil || !strings.Contains(err.Error(), "first failed") {
		t.Fatalf("expected the build-order-first failure, got %v", err)
	}
}

// A base that fails takes its dependents with it: the FROM edge is an ordering
// constraint, and building on a tag that was never written cannot work.
func TestRunBuildDependenciesSkipsDependentsOfAFailedBase(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-ubuntu", "ubuntu:noble"),
		buildSpecFromDockerfile(t, dir, "erun-devops", "erun-ubuntu"),
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	var dependentRan atomic.Bool
	build := func(spec DockerBuildSpec, _, _ io.Writer) error {
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

// The bound is a bound: it exists because each build spawns BuildKit and an
// emulated foreign arch, so exceeding it trades wall-clock for memory pressure.
func TestRunBuildDependenciesNeverExceedsItsBound(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "base", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "dependent", "base"),
	}
	for i := range 6 {
		builds = append(builds, buildSpecFromDockerfile(t, dir, fmt.Sprintf("image-%d", i), "debian:bookworm"))
	}
	ordered, dependencies := resolveTestDependencies(t, builds)

	var running, peak atomic.Int64
	build := func(DockerBuildSpec, io.Writer, io.Writer) error {
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
	if got := resolveBuildJobs(Context{BuildJobs: 1}, 8); got != 1 {
		t.Fatalf("an explicit --jobs 1 must win, got %d", got)
	}
	if got := resolveBuildJobs(Context{}, 8); got != 4 {
		t.Fatalf("the environment override must apply, got %d", got)
	}
	// Never more workers than there is work: the extra ones would only idle.
	if got := resolveBuildJobs(Context{BuildJobs: 16}, 3); got != 3 {
		t.Fatalf("expected the degree capped at the image count, got %d", got)
	}
}
