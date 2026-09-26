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
// thirteen, of which one has a FROM edge onto a sibling — so building them
// strictly one after another spends most of its wall-clock waiting. What must
// stay ordered is narrow and known: a dependent may not start until every image
// it FROMs has finished and written its tags.
//
// An image is therefore dispatched the moment its own bases are done, not when
// every image that happens to share its dependency level is done. The difference
// is not academic: the runtime image FROMs only erun-ubuntu, a cache hit that
// finishes in under a second, and it used to wait out the whole level behind it
// — ten minutes of erun-backend-api, which it never depends on.
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

// buildBaseTags lists the sibling images this Dockerfile FROMs, in Dockerfile
// order, and is the whole of what may hold a build back. A self-reference is not
// a dependency — an image cannot wait for itself, and treating it as one would
// strand it forever.
func buildBaseTags(build DockerBuildSpec, buildsByTag map[string]DockerBuildSpec) []string {
	tag := strings.TrimSpace(build.Image.Tag)
	dependencies := dockerfileLocalBaseImageTags(build.DockerfilePath, buildsByTag)
	bases := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency == tag {
			continue
		}
		bases = append(bases, dependency)
	}
	return bases
}

// buildDependencies maps every image's tag to the sibling bases it must wait
// for.
//
// It also detects a FROM cycle, which the sequential loop never had to: that
// loop tolerated any order the traversal produced, whereas a scheduler waiting
// on bases that are themselves waiting would simply hang. A cycle is reported
// as an error rather than deadlocking.
func buildDependencies(builds []DockerBuildSpec) (map[string][]string, error) {
	buildsByTag := dockerBuildsByTag(builds)
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

// The states a depth-first walk records for a tag: untouched (the map's zero
// value), on the current path — so an edge back to it is the cycle — or cleared.
const (
	buildVisiting = iota + 1
	buildVisited
)

func checkBuildDependenciesAreAcyclic(builds []DockerBuildSpec, dependencies map[string][]string) error {
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

// buildCycleStart finds where the walk re-entered the tag, so the error names
// the images in the cycle rather than every image the walk had passed through.
func buildCycleStart(path []string, tag string) int {
	for i, entry := range path {
		if entry == tag {
			return i
		}
	}
	return 0
}

// traceBuildDependencyPlan records the base ordering the FROM graph implies. It
// is a pure function of the Dockerfiles, so it is the same on every machine —
// unlike the worker count, which is derived from the host and so is deliberately
// not part of the audit line.
//
// It names the edges, not a schedule. A dependent starts when its own bases are
// done, so there is no boundary between one level and the next for a line to
// report, and a line that reported levels would describe a barrier the run does
// not have. A build with nothing to order says nothing: there is no dependency
// to explain, and saying so would churn every independent-image golden.
func traceBuildDependencyPlan(ctx Context, builds []DockerBuildSpec, dependencies map[string][]string) {
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

// runBuildDependencies executes the schedule. Each image is dispatched the
// moment every base it FROMs has finished and written its tags, so an image
// never starts before its bases — the same FROM edges as before, enforced by the
// same graph — but it no longer waits out unrelated images that merely shared
// its dependency level.
//
// The concurrency bound is unchanged: an image waits for its bases without
// holding a worker slot and takes one only when it is ready to build, so the
// pool still holds exactly `jobs` builds.
//
// Output is published in build order — see buildOutputOrder — so a reader sees
// one image's output at a time and two runs of the same build produce the same
// stream. At a degree of one the output streams live instead, which keeps the
// single-job path exactly what it was.
//
// The run's heartbeat is the one deliberate exception, and it is not buffered:
// it goes straight to the run's log stream while builds are still running,
// because a liveness line held back until they finish would be reporting the
// past. It is time-based and therefore not reproducible either, which is why it
// is emitted at default verbosity only by a run that is actually building — see
// build_heartbeat.go.
func runBuildDependencies(ctx Context, builds []DockerBuildSpec, dependencies map[string][]string, build DockerImageBuilderFunc, jobs int) error {
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
	return dispatch.firstFailure()
}

// buildDispatch is one run's dispatch state: the images in build order, the
// worker pool they share, what each image waits for, and how it turned out.
type buildDispatch struct {
	ctx         Context
	builds      []DockerBuildSpec
	build       DockerImageBuilderFunc
	baseIndexes [][]int
	output      *buildOutputOrder
	slots       chan struct{}
	// finished[i] is closed exactly once, when image i is done or is known never
	// to run, so the publisher can walk every image without having to know which
	// of those happened. A base's failure is written before its own channel
	// closes, so a dependent reads it through the close with no further locking.
	finished []chan struct{}
	failures []error
	running  sync.WaitGroup
}

func newBuildDispatch(ctx Context, builds []DockerBuildSpec, dependencies map[string][]string, build DockerImageBuilderFunc, jobs int) *buildDispatch {
	dispatch := &buildDispatch{
		ctx:         ctx,
		builds:      builds,
		build:       build,
		baseIndexes: buildBaseIndexes(builds, dependencies),
		output:      newBuildOutputOrder(ctx.Stdout, ctx.Stderr, len(builds)),
		slots:       make(chan struct{}, jobs),
		finished:    make([]chan struct{}, len(builds)),
		failures:    make([]error, len(builds)),
	}
	for i := range dispatch.finished {
		dispatch.finished[i] = make(chan struct{})
	}
	return dispatch
}

// buildBaseIndexes resolves each image's sibling bases to positions in the build
// order, which is what the dispatch waits on. A base that is not being built —
// an image pulled from a registry — simply has no index and holds nothing back.
func buildBaseIndexes(builds []DockerBuildSpec, dependencies map[string][]string) [][]int {
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
	// The publisher waits for the images, and run waits for the publisher, so the
	// run is not reported finished while output it has already produced is still
	// on its way out.
	published := make(chan struct{})
	go func() {
		defer close(published)
		d.publish()
	}()
	d.running.Wait()
	<-published
}

// publish walks the images in build order: it hands image i its output streams,
// waits for that image to finish, then moves on. Nothing can be published out of
// order, so the stream is the same however the images interleave — the order the
// barrier used to impose by construction.
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
	d.failures[index] = executeDockerBuild(d.ctx, d.builds[index], d.build, d.output.stdoutWriter(index), d.output.stderrWriter(index))
}

// baseFailed waits for every base this image FROMs and reports whether one of
// them failed. A base that failed takes its dependents with it: the FROM edge is
// a real ordering constraint, not an advisory one, and building on a tag that
// was never written cannot work.
//
// This is the only thing that skips work, and it is decided by the graph rather
// than by who lost a race. An image another image's failure could have cancelled
// would make both the work that ran and the failure reported depend on the
// interleaving.
func (d *buildDispatch) baseFailed(index int) bool {
	for _, baseIndex := range d.baseIndexes[index] {
		<-d.finished[baseIndex]
		if d.failures[baseIndex] != nil {
			return true
		}
	}
	return false
}

// firstFailure reports the first failure in build order, so a build that fails
// reports the same error whatever the interleaving happened to be.
func (d *buildDispatch) firstFailure() error {
	for _, failure := range d.failures {
		if failure != nil {
			return failure
		}
	}
	return nil
}

// buildOutputOrder publishes every image's output in build order: image 0's
// stdout and stderr, then image 1's, and so on to the last.
//
// The dependency-level barrier used to impose that order by construction —
// nothing from a later level existed yet when the earlier one was flushed. Once
// images are dispatched independently, something has to own the order, and it is
// owned here rather than left to whichever image finishes first: interleaved
// output from images racing each other is neither readable nor reproducible.
//
// The image whose turn it is streams live, so the build an operator is watching
// is the build they can see; an image that ran ahead of it buffers until its
// turn comes. Deferring a write never reorders one — an image's bytes go out
// whole, in turn — so the published stream is the same either way.
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

// buildOutputStream is one image's half of the published stream: buffered until
// its turn, live after. `dst` is the destination it will be published to, held
// from construction and withheld only until publish hands it over.
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

// publish writes whatever this stream buffered while it waited its turn and then
// marks it live, so nothing is dropped and nothing jumps ahead of an image whose
// turn has not come. A stream with no destination discards the buffer rather
// than holding it: nothing will ever read it.
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
