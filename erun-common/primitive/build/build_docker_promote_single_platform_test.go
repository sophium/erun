package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

const (
	dockerImageIndexJSON       = `[{"Id":"sha256:index0000","Descriptor":{"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json"}}]`
	dockerImageOCIIndexJSON    = `[{"Id":"sha256:index1111","Descriptor":{"mediaType":"application/vnd.oci.image.index.v1+json"}}]`
	dockerImagePlainJSON       = `[{"Id":"sha256:plain0000","Descriptor":{"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}}]`
	dockerImageUndescribedJSON = `[{"Id":"sha256:classic000","RepoTags":["ghcr.io/acme/widget:1.2.3"]}]`
)

type fakeDockerShapes struct {
	indexTags    []string
	ociIndexTags []string
	// undescribedTags have no descriptor, like the classic image store.
	undescribedTags []string
	inspectFails    bool
	unreadableTags  []string
}

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
		"    esac ;;\n"
	if shapes.inspectFails {
		inspectAnswer = "    exit 1\n"
	}
	script := "#!/bin/bash\n" +
		"echo \"$*\" >> " + logPath + "\n" +
		"case \"$1\" in\n" +
		"  image)\n" +
		inspectAnswer +
		"esac\n" +
		"exit 0\n"
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	t.Setenv("ERUN_DOCKER_BIN", path)
	t.Setenv("ERUN_FAKE_DOCKER_LOG", logPath)
}

func shellCasePattern(tags []string) string {
	if len(tags) == 0 {
		return "__no_such_tag__"
	}
	return strings.Join(tags, "|")
}

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

// indexOfExactCall needs an exact match: `docker tag` names both tags in either direction.
func indexOfExactCall(calls []string, want string) int {
	for i, call := range calls {
		if strings.TrimSpace(call) == want {
			return i
		}
	}
	return -1
}

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
	repaired := indexOfExactCall(recorded, "tag "+perArchTag+" "+dockerspec.FingerprintTag(testPromoteBuildInput().Image, "abc123", "linux/amd64"))
	if repaired < 0 {
		t.Fatalf("the rebuilt image was not re-tagged as the fingerprint entry, got %v", recorded)
	}
	if repaired < build {
		t.Fatalf("the fingerprint entry was re-pointed before the rebuild that produces a single-platform image: %v", recorded)
	}
}

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
