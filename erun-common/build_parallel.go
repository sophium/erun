package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Most discovered images are independent of each other — the runtime build has
// nine, of which one has a FROM edge onto a sibling — so building them strictly
// one after another spends most of its wall-clock waiting. What must stay
// ordered is narrow and known: a dependent may not start until every image it
// FROMs has finished and written its tags.
//
// Concurrency is confined to execution. Every trace line is emitted before any
// image builds, sequentially and in dependency order, so the dry-run contract
// and the decision lines are byte-identical whatever the degree of parallelism.

// BuildJobsEnvVar overrides the resolved degree of build concurrency. It exists
// so a scenario can pin the degree rather than inherit the host's core count,
// which would make output depend on the machine that ran it.
const BuildJobsEnvVar = "ERUN_BUILD_JOBS"

// resolveBuildJobs picks how many images may build at once. Each build spawns
// BuildKit and the foreign architecture runs under emulation, which is memory
// heavy, so the automatic ceiling is half the cores rather than all of them —
// oversubscribing here trades wall-clock for swap.
func resolveBuildJobs(ctx Context, images int) int {
	jobs := ctx.BuildJobs
	if jobs <= 0 {
		if fromEnv, err := strconv.Atoi(strings.TrimSpace(os.Getenv(BuildJobsEnvVar))); err == nil && fromEnv > 0 {
			jobs = fromEnv
		}
	}
	if jobs <= 0 {
		jobs = max(2, runtime.NumCPU()/2)
	}
	return min(jobs, max(1, images))
}

// buildWave is one topological level: every image in it can build at the same
// time, and none of them may start before the previous wave has finished.
type buildWave []DockerBuildSpec

// resolveBuildWaves groups the builds into dependency levels.
//
// It also detects a FROM cycle, which the sequential loop never had to: that
// loop tolerated any order the traversal produced, whereas a wave scheduler
// waiting on parents that are themselves waiting would simply hang. A cycle is
// reported as an error rather than deadlocking.
func resolveBuildWaves(builds []DockerBuildSpec) ([]buildWave, error) {
	if len(builds) < 2 {
		if len(builds) == 0 {
			return nil, nil
		}
		return []buildWave{buildWave(builds)}, nil
	}

	buildsByTag := make(map[string]DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}

	pending := make([]DockerBuildSpec, len(builds))
	copy(pending, builds)
	done := make(map[string]struct{}, len(builds))

	waves := make([]buildWave, 0, len(builds))
	for len(pending) > 0 {
		wave := buildWave{}
		remaining := pending[:0:0]
		for _, build := range pending {
			if buildBasesAreBuilt(build, buildsByTag, done) {
				wave = append(wave, build)
				continue
			}
			remaining = append(remaining, build)
		}
		if len(wave) == 0 {
			return nil, fmt.Errorf("docker builds have a FROM cycle among %s", describeBuildTags(pending))
		}
		for _, build := range wave {
			done[strings.TrimSpace(build.Image.Tag)] = struct{}{}
		}
		waves = append(waves, wave)
		pending = remaining
	}
	return waves, nil
}

// buildBasesAreBuilt reports whether every sibling this image FROMs has already
// been built. A self-reference is not a dependency — an image cannot wait for
// itself, and treating it as one would strand it forever.
func buildBasesAreBuilt(build DockerBuildSpec, buildsByTag map[string]DockerBuildSpec, done map[string]struct{}) bool {
	tag := strings.TrimSpace(build.Image.Tag)
	for _, dependency := range dockerfileLocalBaseImageTags(build.DockerfilePath, buildsByTag) {
		if dependency == tag {
			continue
		}
		if _, built := done[dependency]; !built {
			return false
		}
	}
	return true
}

func describeBuildTags(builds []DockerBuildSpec) string {
	tags := make([]string, 0, len(builds))
	for _, build := range builds {
		tags = append(tags, strings.TrimSpace(build.Image.Tag))
	}
	return strings.Join(tags, ", ")
}

// traceBuildWavePlan records the schedule the FROM graph implies. It is a pure
// function of the Dockerfiles, so it is the same on every machine — unlike the
// worker count, which is derived from the host and so is deliberately not part
// of the audit line. A single-wave build says nothing: there is no schedule to
// explain, and saying so would churn every single-image golden.
func traceBuildWavePlan(ctx Context, waves []buildWave) {
	if len(waves) < 2 {
		return
	}
	total := 0
	for _, wave := range waves {
		total += len(wave)
	}
	parts := make([]string, 0, len(waves))
	for i, wave := range waves {
		parts = append(parts, fmt.Sprintf("wave %d (%d): %s", i+1, len(wave), describeBuildTags(wave)))
	}
	ctx.Trace(fmt.Sprintf("build: %d images in %d waves — %s", total, len(waves), strings.Join(parts, "; ")))
}

// runBuildWaves executes the schedule. Within a wave the builds run
// concurrently up to the resolved degree; the wave boundary is the barrier that
// enforces the FROM edges, because a dependent must not start until its base has
// written its tags.
//
// Output is buffered per image and flushed in the wave's own order once the wave
// finishes, so a reader sees one image's output at a time and two runs of the
// same build produce the same stream. At a degree of one the output streams live
// instead, which keeps the single-job path exactly what it was.
func runBuildWaves(ctx Context, waves []buildWave, build DockerImageBuilderFunc, jobs int) error {
	for _, wave := range waves {
		if err := runBuildWave(ctx, wave, build, jobs); err != nil {
			return err
		}
	}
	return nil
}

func runBuildWave(ctx Context, wave buildWave, build DockerImageBuilderFunc, jobs int) error {
	if jobs <= 1 || len(wave) == 1 {
		for _, buildInput := range wave {
			if err := executeDockerBuild(ctx, buildInput, build, ctx.Stdout, ctx.Stderr); err != nil {
				return err
			}
		}
		return nil
	}

	type outcome struct {
		stdout bytes.Buffer
		stderr bytes.Buffer
		err    error
	}
	outcomes := make([]outcome, len(wave))
	slots := make(chan struct{}, jobs)
	var group sync.WaitGroup

	for i, buildInput := range wave {
		group.Add(1)
		go func(index int, spec DockerBuildSpec) {
			defer group.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			outcomes[index].err = executeDockerBuild(ctx, spec, build, &outcomes[index].stdout, &outcomes[index].stderr)
		}(i, buildInput)
	}
	group.Wait()

	// Flushed in wave order regardless of who finished first, then the first
	// failure in that same order is returned — so a build that fails reports the
	// same error whatever the interleaving happened to be.
	var failure error
	for i := range outcomes {
		writeBuffered(ctx.Stdout, &outcomes[i].stdout)
		writeBuffered(ctx.Stderr, &outcomes[i].stderr)
		if outcomes[i].err != nil && failure == nil {
			failure = outcomes[i].err
		}
	}
	return failure
}

func writeBuffered(dst io.Writer, buffer *bytes.Buffer) {
	if dst == nil || buffer.Len() == 0 {
		return
	}
	_, _ = dst.Write(buffer.Bytes())
}
