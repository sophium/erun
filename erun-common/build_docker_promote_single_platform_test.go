package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A `-amd64` tag has to denote a single-platform manifest: `docker manifest
// create` reads every per-arch tag back from the registry and rejects one that
// is itself a manifest list ("<tag> is a manifest list"), which kills a release
// before any image work happens.
//
// The build path keeps that invariant structurally, with --provenance=false on
// the build itself. The promote path has no build behind it — it re-tags a
// cached image — and on a daemon backed by the containerd image store that
// cached image is a whole multi-platform index, so publishing it puts a
// manifest list under a per-arch name. These tests pin both halves of the
// invariant and the repair for a cache entry that cannot meet it.

const (
	dockerImageIndexJSON    = `[{"Id":"sha256:index0000","Descriptor":{"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json"}}]`
	dockerImageOCIIndexJSON = `[{"Id":"sha256:index1111","Descriptor":{"mediaType":"application/vnd.oci.image.index.v1+json"}}]`
	dockerImagePlainJSON    = `[{"Id":"sha256:plain0000","Descriptor":{"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}}]`
	// A classic-store image carries no descriptor at all.
	dockerImageUndescribedJSON = `[{"Id":"sha256:classic000","RepoTags":["ghcr.io/acme/widget:1.2.3"]}]`
)

// fakeDockerShapes names, per tag, the daemon state the promote path reads.
type fakeDockerShapes struct {
	// indexTags resolve, locally, to a docker manifest list descriptor.
	indexTags []string
	// ociIndexTags resolve, locally, to an OCI image index descriptor.
	ociIndexTags []string
	// undescribedTags resolve with no descriptor at all, the shape the classic
	// image store answers with.
	undescribedTags []string
	// inspectFails makes `docker image inspect` exit non-zero, the shape an
	// older or unreachable daemon answers with.
	inspectFails bool
	// unreadableTags answer `docker image inspect` with output that is not JSON.
	unreadableTags []string
	// missingTags are tags the local store does not hold. `docker tag` from one
	// fails with the daemon's own answer for an absent source, verbatim.
	missingTags []string
}

// newRecordingFakeDocker installs a stub `docker` (via the ERUN_DOCKER_BIN
// seam) that records every invocation and answers, per tag, whether the local
// image is a multi-platform index. Every other subcommand succeeds.
func newRecordingFakeDocker(t *testing.T, shapes fakeDockerShapes) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	inspectAnswer := "    case \"${@: -1}\" in\n" +
		"      " + shellCasePattern(shapes.indexTags) + ") echo '" + dockerImageIndexJSON + "' ;;\n" +
		"      " + shellCasePattern(shapes.ociIndexTags) + ") echo '" + dockerImageOCIIndexJSON + "' ;;\n" +
		"      " + shellCasePattern(shapes.undescribedTags) + ") echo '" + dockerImageUndescribedJSON + "' ;;\n" +
		"      " + shellCasePattern(shapes.unreadableTags) + ") echo 'not json' ;;\n" +
		"      *) echo '" + dockerImagePlainJSON + "' ;;\n" +
		"    esac\n"
	if shapes.inspectFails {
		inspectAnswer = "    exit 1\n"
	}
	// `docker tag <source> <target>` re-tags a source the store no longer holds
	// the way the daemon does, so a promote built on a stale decision reaches the
	// same failure a real release hits.
	tagAnswer := "    case \"$2\" in\n" +
		"      " + shellCasePattern(shapes.missingTags) + ")\n" +
		"        echo \"Error response from daemon: No such image: $2\" >&2\n" +
		"        exit 1\n" +
		"        ;;\n" +
		"    esac\n"
	script := "#!/bin/bash\n" +
		"echo \"$*\" >> " + logPath + "\n" +
		"case \"$1\" in\n" +
		"  image)\n" +
		inspectAnswer +
		"    ;;\n" +
		"  tag)\n" +
		tagAnswer +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	t.Setenv("ERUN_DOCKER_BIN", path)
	t.Setenv("ERUN_FAKE_DOCKER_LOG", logPath)
}

// shellCasePattern renders tags as one shell `case` alternation, with a pattern
// that matches nothing when there are none.
func shellCasePattern(tags []string) string {
	if len(tags) == 0 {
		return "__no_such_tag__"
	}
	return strings.Join(tags, "|")
}

// dockerCallsFrom reads every recorded invocation, joined back into one string
// per call. Re-read after the code under test has run.
func dockerCallsFrom(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("ERUN_FAKE_DOCKER_LOG"))
	if err != nil {
		t.Fatalf("read fake docker log: %v", err)
	}
	calls := make([]string, 0, 8)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			calls = append(calls, line)
		}
	}
	return calls
}

// indexOfArgv returns the position of the first recorded call whose joined
// arguments contain every needle, or -1.
func indexOfArgv(calls []string, needles ...string) int {
	for i, call := range calls {
		missing := false
		for _, needle := range needles {
			if !strings.Contains(call, needle) {
				missing = true
				break
			}
		}
		if !missing {
			return i
		}
	}
	return -1
}

// dockerCallsMatching returns every recorded call whose joined arguments
// contain all the given needles.
func dockerCallsMatching(calls []string, needles ...string) []string {
	matched := make([]string, 0, len(calls))
	for _, call := range calls {
		missing := false
		for _, needle := range needles {
			if !strings.Contains(call, needle) {
				missing = true
				break
			}
		}
		if !missing {
			matched = append(matched, call)
		}
	}
	return matched
}

// indexOfExactCall returns the position of the first recorded call whose joined
// arguments are exactly want, or -1. `docker tag` takes both tags of interest
// on one line in either direction, so a substring match cannot tell the
// promote's re-tag from the repair that follows a rebuild.
func indexOfExactCall(calls []string, want string) int {
	for i, call := range calls {
		if strings.TrimSpace(call) == want {
			return i
		}
	}
	return -1
}

// The build half of the invariant.
func TestBuildPathPublishesEveryPlatformWithoutProvenanceAttestation(t *testing.T) {
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		args := dockerBuildArgs(DockerBuildSpec{Image: DockerImageReference{Tag: "ghcr.io/acme/widget:1.2.3"}}, platform)
		if !slicesContains(args, "--provenance=false") {
			t.Fatalf("platform %s must not attach the provenance attestation that makes a per-arch tag a manifest list, got %v", platform, args)
		}
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// The promote half: a cache entry that is a whole multi-platform index cannot be
// re-tagged into a per-arch tag, so that platform is built for real — which is
// also what re-points the cache entry at a single-platform image, so the next
// run can promote it.
func TestPromoteRebuildsACacheEntryThatIsAWholeMultiPlatformIndex(t *testing.T) {
	perArchTag := "ghcr.io/sophium/erun-console:1.0.246-amd64"
	newRecordingFakeDocker(t, fakeDockerShapes{indexTags: []string{perArchTag}})

	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr); err != nil {
		t.Fatalf("promote: %v", err)
	}

	recorded := dockerCallsFrom(t)
	build := indexOfArgv(recorded, "build", "--platform linux/amd64")
	if build < 0 {
		t.Fatalf("expected the platform to be rebuilt from source, got %v", recorded)
	}
	// The index must never reach the registry: the only push of this tag is the
	// one that publishes the image the rebuild just produced.
	pushes := dockerCallsMatching(recorded, "push", perArchTag)
	if len(pushes) != 1 {
		t.Fatalf("expected exactly one push of %s, got %v", perArchTag, pushes)
	}
	if indexOfArgv(recorded, "push", perArchTag) < build {
		t.Fatalf("the cache entry's index was pushed before anything rebuilt it: %v", recorded)
	}
	if !strings.Contains(stderr.String(), "would publish a multi-platform image under a per-arch tag") ||
		!strings.Contains(stderr.String(), perArchTag) {
		t.Fatalf("expected the rebuild to be reported against the tag it repairs, got: %s", stderr.String())
	}
	// Rebuilding is also what repairs the cache: the fingerprint entry is
	// re-pointed at the single-platform image the build just produced, so the
	// next run has an entry this check can promote instead of rebuilding again.
	repaired := indexOfExactCall(recorded, "tag "+perArchTag+" "+fingerprintTag(testPromoteBuildInput().Image, "abc123", "linux/amd64"))
	if repaired < 0 {
		t.Fatalf("the rebuilt image was not re-tagged as the fingerprint entry, got %v", recorded)
	}
	if repaired < build {
		t.Fatalf("the fingerprint entry was re-pointed before the rebuild that produces a single-platform image: %v", recorded)
	}
}

// The fail-safe direction the shape check rests on: a daemon that cannot answer
// — an inspect that fails, output that is not JSON — is not evidence of a
// multi-platform image. Reading it as one would trade a working promote for a
// full rebuild on every daemon that cannot say.
func TestPromotePublishesWhenTheImageShapeCannotBeRead(t *testing.T) {
	perArchTag := "ghcr.io/sophium/erun-console:1.0.246-amd64"
	cases := map[string]fakeDockerShapes{
		"inspect fails":      {inspectFails: true},
		"output is not JSON": {unreadableTags: []string{perArchTag}},
	}
	for name, shapes := range cases {
		t.Run(name, func(t *testing.T) {
			newRecordingFakeDocker(t, shapes)

			var stdout, stderr bytes.Buffer
			if err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr); err != nil {
				t.Fatalf("promote: %v", err)
			}

			calls := dockerCallsFrom(t)
			if pushes := dockerCallsMatching(calls, "push", perArchTag); len(pushes) != 1 {
				t.Fatalf("expected the cache entry to be promoted, got %v", calls)
			}
			if builds := dockerCallsMatching(calls, "build", "linux/amd64"); len(builds) != 0 {
				t.Fatalf("an unreadable image shape must not force a rebuild, got %v", builds)
			}
		})
	}
}

// The other way a cache entry cannot be promoted: the local store no longer has
// it. A release decides up front and then spends minutes building the rest of
// its images, and a daemon reclaiming space evicts the very content the decision
// was based on, so the promote's re-tag fails with "No such image: <fp tag>" —
// after every other image in the release has been built. That is a cache miss,
// not a failure: the platform is built and pushed for real, and the rebuild also
// re-points the fingerprint entry, so the next run's decision is honest again.
func TestPromoteRebuildsWhenTheLocalStoreLosesTheFingerprintImage(t *testing.T) {
	perArchTag := "ghcr.io/sophium/erun-console:1.0.246-amd64"
	fpTag := fingerprintTag(testPromoteBuildInput().Image, "abc123", "linux/amd64")
	newRecordingFakeDocker(t, fakeDockerShapes{missingTags: []string{fpTag}})

	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr); err != nil {
		t.Fatalf("a fingerprint image the local store lost is a cache miss, not a failure, got: %v", err)
	}

	recorded := dockerCallsFrom(t)
	build := indexOfArgv(recorded, "build", "--platform linux/amd64")
	if build < 0 {
		t.Fatalf("expected the platform to be rebuilt from source, got %v", recorded)
	}
	// The promoted tag must only ever be published from the rebuild: a promote
	// that could not re-tag anything has nothing of its own to push.
	if pushes := dockerCallsMatching(recorded, "push", perArchTag); len(pushes) != 1 {
		t.Fatalf("expected exactly one push of %s, got %v", perArchTag, pushes)
	}
	if indexOfArgv(recorded, "push", perArchTag) < build {
		t.Fatalf("the absent cache entry was pushed before anything rebuilt it: %v", recorded)
	}
	if !strings.Contains(stderr.String(), "the local store no longer has the fingerprint image it was decided from") ||
		!strings.Contains(stderr.String(), perArchTag) {
		t.Fatalf("expected the rebuild to name the tag and the reason, got: %s", stderr.String())
	}
	// The rebuild is what repairs the cache the next run reads.
	repaired := indexOfExactCall(recorded, "tag "+perArchTag+" "+fpTag)
	if repaired < build {
		t.Fatalf("the fingerprint entry was not re-pointed after the rebuild that produces it: %v", recorded)
	}
}

// Both spellings of an index, and the classic store's descriptor-less answer,
// which has to stay on the path it published on before.
func TestLocalImageIsSinglePlatformCoversBothSpellings(t *testing.T) {
	listTag := "ghcr.io/sophium/erun-console:1.0.246-amd64"
	ociTag := "ghcr.io/sophium/erun-console:1.0.247-amd64"
	classicTag := "ghcr.io/sophium/erun-console:1.0.248-amd64"
	newRecordingFakeDocker(t, fakeDockerShapes{
		indexTags:       []string{listTag},
		ociIndexTags:    []string{ociTag},
		undescribedTags: []string{classicTag},
	})

	for _, tag := range []string{listTag, ociTag} {
		if localImageIsSinglePlatform(tag) {
			t.Fatalf("%s is a multi-platform index and must not read as single-platform", tag)
		}
	}
	if !localImageIsSinglePlatform(classicTag) {
		t.Fatalf("%s reports no descriptor and must read as single-platform", classicTag)
	}
}

// A single-platform cache entry — every image on the classic store — publishes
// exactly as it did before, with no extra rebuild.
func TestPromotePublishesAPlainCacheEntryWithoutRebuilding(t *testing.T) {
	perArchTag := "ghcr.io/sophium/erun-console:1.0.246-amd64"
	newRecordingFakeDocker(t, fakeDockerShapes{})

	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr); err != nil {
		t.Fatalf("promote: %v", err)
	}

	calls := dockerCallsFrom(t)
	if pushes := dockerCallsMatching(calls, "push", perArchTag); len(pushes) != 1 {
		t.Fatalf("expected one push of %s, got %v", perArchTag, pushes)
	}
	if builds := dockerCallsMatching(calls, "build", "linux/amd64"); len(builds) != 0 {
		t.Fatalf("a single-platform cache entry must not rebuild, got %v", builds)
	}
	if strings.Contains(stderr.String(), "rebuilding from source") {
		t.Fatalf("a plain promote must not report a rebuild, got: %s", stderr.String())
	}
}

// The check is per platform: a sibling cache entry that is a real platform
// image still promotes, so one index in a project does not rebuild the rest.
func TestPromoteRebuildsOnlyThePlatformWhoseCacheEntryIsAnIndex(t *testing.T) {
	amd64Tag := "ghcr.io/sophium/erun-console:1.0.246-amd64"
	arm64Tag := "ghcr.io/sophium/erun-console:1.0.246-arm64"
	newRecordingFakeDocker(t, fakeDockerShapes{indexTags: []string{amd64Tag}})

	spec := testPromoteBuildInput()
	spec.Platforms = []string{"linux/amd64", "linux/arm64"}
	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(spec, &stdout, &stderr); err != nil {
		t.Fatalf("promote: %v", err)
	}

	recorded := dockerCallsFrom(t)
	if builds := dockerCallsMatching(recorded, "build", "--platform linux/amd64"); len(builds) != 1 {
		t.Fatalf("expected amd64 to be rebuilt from source, got %v (all calls: %v)", builds, recorded)
	}
	if arm64Builds := dockerCallsMatching(recorded, "build", "--platform linux/arm64"); len(arm64Builds) != 0 {
		t.Fatalf("arm64's cache entry is a platform image and must not be rebuilt, got %v", arm64Builds)
	}
	if pushes := dockerCallsMatching(recorded, "push", arm64Tag); len(pushes) != 1 {
		t.Fatalf("expected arm64 to be promoted, got %v", pushes)
	}
}
