package run

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	orderpkg "github.com/sophium/erun/erun-common/primitive/build/order"
)

// An image is dispatched as soon as its own FROM bases finish. Trace lines are emitted
// sequentially before any build, so output is identical at any degree of parallelism.

// BuildJobsEnvVar pins build concurrency so output does not depend on the host's core count.
const BuildJobsEnvVar = "ERUN_BUILD_JOBS"

// resolveBuildJobs picks how many images build at once. Emulated foreign-arch BuildKit is
// memory heavy, so the automatic ceiling is half the cores.
func resolveBuildJobs(ctx core.Context, images int) int {
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

// buildBaseTags lists the sibling images this Dockerfile FROMs, in Dockerfile order.
// A self-reference is ignored; it would strand the image forever.
func buildBaseTags(build imagebuild.DockerBuildSpec, buildsByTag map[string]imagebuild.DockerBuildSpec) []string {
	tag := strings.TrimSpace(build.Image.Tag)
	dependencies := orderpkg.DockerfileLocalBaseImageTags(build.DockerfilePath, buildsByTag)
	bases := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency == tag {
			continue
		}
		bases = append(bases, dependency)
	}
	return bases
}

// buildDependencies maps each tag to its sibling bases and reports FROM cycles instead of deadlocking.
func buildDependencies(builds []imagebuild.DockerBuildSpec) (map[string][]string, error) {
	buildsByTag := orderpkg.DockerBuildsByTag(builds)
	dependencies := make(map[string][]string, len(builds))
	for _, build := range builds {
		tag := strings.TrimSpace(build.Image.Tag)
		dependencies[tag] = buildBaseTags(build, buildsByTag)
	}
	if err := checkBuildDependenciesAreAcyclic(builds, dependencies); err != nil {
		return nil, err
	}
	return dependencies, nil
}

const (
	buildVisiting = iota + 1
	buildVisited
)

func checkBuildDependenciesAreAcyclic(builds []imagebuild.DockerBuildSpec, dependencies map[string][]string) error {
	state := make(map[string]int, len(dependencies))
	path := make([]string, 0, len(dependencies))

	var visit func(tag string) error
	visit = func(tag string) error {
		switch state[tag] {
		case buildVisited:
			return nil
		case buildVisiting:
			return fmt.Errorf("docker builds have a FROM cycle among %s", strings.Join(path[buildCycleStart(path, tag):], ", "))
		}
		state[tag] = buildVisiting
		path = append(path, tag)
		for _, base := range dependencies[tag] {
			if _, known := dependencies[base]; !known {
				continue
			}
			if err := visit(base); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[tag] = buildVisited
		return nil
	}

	for _, build := range builds {
		if err := visit(strings.TrimSpace(build.Image.Tag)); err != nil {
			return err
		}
	}
	return nil
}

func buildCycleStart(path []string, tag string) int {
	for i, entry := range path {
		if entry == tag {
			return i
		}
	}
	return 0
}

// traceBuildDependencyPlan records the FROM edges, not a schedule; worker count is host-derived
// and deliberately omitted. Nothing to order prints nothing.
func traceBuildDependencyPlan(ctx core.Context, builds []imagebuild.DockerBuildSpec, dependencies map[string][]string) {
	waiting := make([]string, 0, len(builds))
	for _, build := range builds {
		tag := strings.TrimSpace(build.Image.Tag)
		if bases := dependencies[tag]; len(bases) > 0 {
			waiting = append(waiting, fmt.Sprintf("%s after %s", tag, strings.Join(bases, ", ")))
		}
	}
	if len(waiting) == 0 {
		return
	}
	ctx.Trace(fmt.Sprintf("build: %d images, %d waiting on a sibling base — %s", len(builds), len(waiting), strings.Join(waiting, "; ")))
}

// runBuildDependencies runs each image once its bases finish, holding at most `jobs` worker
// slots. Output is published in build order; the heartbeat bypasses that buffering.
func runBuildDependencies(ctx core.Context, builds []imagebuild.DockerBuildSpec, dependencies map[string][]string, build imagebuild.DockerImageBuilderFunc, jobs int) error {
	if jobs <= 1 || len(builds) <= 1 {
		for _, buildInput := range builds {
			if err := executeDockerBuild(ctx, buildInput, build, ctx.Stdout, ctx.Stderr); err != nil {
				return err
			}
		}
		return nil
	}

	dispatch := newBuildDispatch(ctx, builds, dependencies, build, jobs)
	dispatch.run()
	for i, buildInput := range builds {
		if dispatch.outcomes[i] != "" {
			reportDockerBuildTestOutcome(ctx, buildInput, dispatch.outcomes[i])
		}
	}
	return dispatch.firstFailure()
}

type buildDispatch struct {
	ctx         core.Context
	builds      []imagebuild.DockerBuildSpec
	build       imagebuild.DockerImageBuilderFunc
	baseIndexes [][]int
	output      *buildOutputOrder
	slots       chan struct{}
	// finished[i] is closed once image i is done or will never run. A base's failure is written
	// before its channel closes, so dependents read it without locking.
	finished []chan struct{}
	failures []error
	outcomes []string
	running  sync.WaitGroup
}

func newBuildDispatch(ctx core.Context, builds []imagebuild.DockerBuildSpec, dependencies map[string][]string, build imagebuild.DockerImageBuilderFunc, jobs int) *buildDispatch {
	dispatch := &buildDispatch{
		ctx:         ctx,
		builds:      builds,
		build:       build,
		baseIndexes: buildBaseIndexes(builds, dependencies),
		output:      newBuildOutputOrder(ctx.Stdout, ctx.Stderr, len(builds)),
		slots:       make(chan struct{}, jobs),
		finished:    make([]chan struct{}, len(builds)),
		failures:    make([]error, len(builds)),
		outcomes:    make([]string, len(builds)),
	}
	for i := range dispatch.finished {
		dispatch.finished[i] = make(chan struct{})
	}
	return dispatch
}

// A base not built in this run (pulled from a registry) has no index and holds nothing back.
func buildBaseIndexes(builds []imagebuild.DockerBuildSpec, dependencies map[string][]string) [][]int {
	indexByTag := make(map[string]int, len(builds))
	for i, buildInput := range builds {
		indexByTag[strings.TrimSpace(buildInput.Image.Tag)] = i
	}
	baseIndexes := make([][]int, len(builds))
	for i, buildInput := range builds {
		for _, base := range dependencies[strings.TrimSpace(buildInput.Image.Tag)] {
			if baseIndex, ok := indexByTag[base]; ok {
				baseIndexes[i] = append(baseIndexes[i], baseIndex)
			}
		}
	}
	return baseIndexes
}

func (d *buildDispatch) run() {
	for i := range d.builds {
		d.running.Add(1)
		go d.buildImage(i)
	}
	published := make(chan struct{})
	go func() {
		defer close(published)
		d.publish()
	}()
	d.running.Wait()
	// Wait for the publisher too, so output still in flight is not cut off.
	<-published
}

func (d *buildDispatch) publish() {
	for i := range d.builds {
		d.output.publishStdout(i)
		<-d.finished[i]
		d.output.publishStderr(i)
	}
}

func (d *buildDispatch) buildImage(index int) {
	defer d.running.Done()
	defer close(d.finished[index])
	if d.baseFailed(index) {
		return
	}
	d.slots <- struct{}{}
	defer func() { <-d.slots }()
	outcome, err := runDockerBuildStep(d.ctx, d.builds[index], d.build, d.output.stdoutWriter(index), d.output.stderrWriter(index))
	if err == nil {
		d.outcomes[index] = outcome
	}
	d.failures[index] = err
}

// baseFailed waits for this image's bases and reports whether one failed. Skips are decided
// by the graph, never by race order, so results don't depend on interleaving.
func (d *buildDispatch) baseFailed(index int) bool {
	for _, baseIndex := range d.baseIndexes[index] {
		<-d.finished[baseIndex]
		if d.failures[baseIndex] != nil {
			return true
		}
	}
	return false
}

func (d *buildDispatch) firstFailure() error {
	for _, failure := range d.failures {
		if failure != nil {
			return failure
		}
	}
	return nil
}

// buildOutputOrder publishes each image's output whole, in build order. The image whose turn
// it is streams live; images that ran ahead buffer until their turn.
type buildOutputOrder struct {
	stdout []*buildOutputStream
	stderr []*buildOutputStream
}

func newBuildOutputOrder(stdout, stderr io.Writer, images int) *buildOutputOrder {
	order := &buildOutputOrder{
		stdout: make([]*buildOutputStream, images),
		stderr: make([]*buildOutputStream, images),
	}
	for i := range order.stdout {
		order.stdout[i] = &buildOutputStream{dst: stdout}
		order.stderr[i] = &buildOutputStream{dst: stderr}
	}
	return order
}

func (o *buildOutputOrder) stdoutWriter(index int) io.Writer { return o.stdout[index] }
func (o *buildOutputOrder) stderrWriter(index int) io.Writer { return o.stderr[index] }

func (o *buildOutputOrder) publishStdout(index int) { o.stdout[index].publish() }
func (o *buildOutputOrder) publishStderr(index int) { o.stderr[index].publish() }

type buildOutputStream struct {
	mu   sync.Mutex
	dst  io.Writer
	live bool
	buf  bytes.Buffer
}

func (s *buildOutputStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.live {
		return s.buf.Write(p)
	}
	if s.dst == nil {
		return len(p), nil
	}
	return s.dst.Write(p)
}

// A stream with no destination discards its buffer: nothing will read it.
func (s *buildOutputStream) publish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live {
		return
	}
	s.live = true
	if s.dst != nil && s.buf.Len() > 0 {
		_, _ = s.dst.Write(s.buf.Bytes())
	}
	s.buf.Reset()
}
