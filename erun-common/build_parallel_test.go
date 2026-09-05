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

// Independent images share a wave; one that FROMs a sibling waits for it. This
// is the whole scheduling contract — everything else is a bounded pool.
func TestResolveBuildWavesGroupsIndependentImagesTogether(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "erun-devops", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "erun-mcp", "erun-devops"),
		buildSpecFromDockerfile(t, dir, "erun-api", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "erun-console", "node:22"),
	}

	waves, err := resolveBuildWaves(orderedDockerBuildSpecs(builds))
	if err != nil {
		t.Fatalf("resolve waves: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("expected two waves, got %d: %v", len(waves), describeWaves(waves))
	}
	if len(waves[0]) != 3 {
		t.Fatalf("expected the three independent images to share wave 1: %v", describeWaves(waves))
	}
	if len(waves[1]) != 1 || strings.TrimSpace(waves[1][0].Image.Tag) != "erun-mcp" {
		t.Fatalf("erun-mcp must wait for erun-devops: %v", describeWaves(waves))
	}
}

// A single image has nothing to schedule, and must not be described as though it
// did — every single-image project's output would churn for no information.
func TestResolveBuildWavesSaysNothingAboutASingleImage(t *testing.T) {
	dir := t.TempDir()
	waves, err := resolveBuildWaves([]DockerBuildSpec{buildSpecFromDockerfile(t, dir, "solo", "debian:bookworm")})
	if err != nil {
		t.Fatalf("resolve waves: %v", err)
	}
	var trace bytes.Buffer
	traceBuildWavePlan(Context{Logger: NewLoggerWithWriters(0, &trace, &trace)}, waves)
	if trace.Len() != 0 {
		t.Fatalf("a one-wave build has no schedule to explain, got %q", trace.String())
	}
}

// The sequential loop tolerated a FROM cycle by simply picking an order. A wave
// scheduler would wait forever on parents that are waiting on it, so the cycle
// has to be found and reported rather than hung on.
func TestResolveBuildWavesReportsAFromCycleInsteadOfHanging(t *testing.T) {
	dir := t.TempDir()
	builds := []DockerBuildSpec{
		buildSpecFromDockerfile(t, dir, "alpha", "beta"),
		buildSpecFromDockerfile(t, dir, "beta", "alpha"),
	}
	_, err := resolveBuildWaves(builds)
	if err == nil {
		t.Fatal("expected a FROM cycle to be reported")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("the error must name the cycle, got %v", err)
	}
}

// Concurrency must not reach the reader: two runs of the same wave produce the
// same stream, and each image's output stays in one piece rather than
// interleaving with its neighbours.
func TestRunBuildWaveFlushesOutputInWaveOrderWhateverFinishesFirst(t *testing.T) {
	dir := t.TempDir()
	wave := buildWave{
		buildSpecFromDockerfile(t, dir, "slow", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "fast", "debian:bookworm"),
	}

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
	ctx := Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard), Stdout: &out, Stderr: io.Discard}
	if err := runBuildWave(ctx, wave, build, 2); err != nil {
		t.Fatalf("run wave: %v", err)
	}
	if got := out.String(); got != "building slow\nbuilding fast\n" {
		t.Fatalf("output must follow the wave's order, got %q", got)
	}
}

// Failures are reported in the wave's order too, so which error a build reports
// does not depend on which goroutine lost the race.
func TestRunBuildWaveReportsTheFirstFailureInWaveOrder(t *testing.T) {
	dir := t.TempDir()
	wave := buildWave{
		buildSpecFromDockerfile(t, dir, "first", "debian:bookworm"),
		buildSpecFromDockerfile(t, dir, "second", "debian:bookworm"),
	}
	build := func(spec DockerBuildSpec, _, _ io.Writer) error {
		return errors.New(strings.TrimSpace(spec.Image.Tag) + " failed")
	}
	ctx := Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard), Stdout: io.Discard, Stderr: io.Discard}
	err := runBuildWave(ctx, wave, build, 2)
	if err == nil || !strings.Contains(err.Error(), "first failed") {
		t.Fatalf("expected the wave-order-first failure, got %v", err)
	}
}

// The bound is a bound: it exists because each build spawns BuildKit and an
// emulated foreign arch, so exceeding it trades wall-clock for memory pressure.
func TestRunBuildWaveNeverExceedsItsBound(t *testing.T) {
	dir := t.TempDir()
	wave := buildWave{}
	for i := range 6 {
		wave = append(wave, buildSpecFromDockerfile(t, dir, fmt.Sprintf("image-%d", i), "debian:bookworm"))
	}

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
	ctx := Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard), Stdout: io.Discard, Stderr: io.Discard}
	if err := runBuildWave(ctx, wave, build, 2); err != nil {
		t.Fatalf("run wave: %v", err)
	}
	if peak.Load() > 2 {
		t.Fatalf("ran %d builds at once, bound was 2", peak.Load())
	}
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

func describeWaves(waves []buildWave) []string {
	out := make([]string, 0, len(waves))
	for _, wave := range waves {
		out = append(out, describeBuildTags(wave))
	}
	return out
}
