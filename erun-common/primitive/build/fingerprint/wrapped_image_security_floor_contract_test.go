package fingerprint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The first docker release whose dockerd, containerd, runc, BuildKit and CLI carry the
// fixes a scan of 28.1.1 reports; docker's own advisories are fixed only from 29.3.1.
const dockerEngineSecurityFloor = "29.9.2"

// The buildx docker:29.9.2-cli ships, the first past v0.18.0's advisories.
const buildxSecurityFloor = "v0.38.1"

// The first upstream release each wrapped service image may pin. A wrapper's VERSION names
// the same release, optionally followed by erun's own "-<revision>" of the wrapper.
var wrappedImageSecurityFloors = []struct {
	image, arg, floor, fixes string
}{
	{"erun-dind", "DOCKER_VERSION", dockerEngineSecurityFloor, "the moby, BuildKit, containerd, runc and grpc advisories every 28.x release carries"},
	{"erun-backend-postgres", "POSTGRES_VERSION", "18.6", "the PostgreSQL 18.4 and 18.6 security advisories and ships Debian 13's openssl, gnutls, perl, krb5 and util-linux fixes"},
	{"erun-oci-registry", "ZOT_VERSION", "v2.1.22", "the containerd, distribution, grpc, go-git and x/crypto advisories of v2.1.20 and v2.1.21"},
}

// An apk floor holds only on the Alpine branch it was read from: 3.21's brotli-libs>=1.1.1-r0
// is met by 3.24's vulnerable 1.2.0-r1. A new DOCKER_VERSION fails here until its base is checked.
const dindAlpineFloorsCheckedOn = "29.9.2"

var dindAlpinePackageFloors = map[string]string{
	"brotli-libs": "1.2.1-r0",
	"libcurl":     "8.23.0-r0",
}

var (
	apkFloorConstraintPattern   = regexp.MustCompile(`'([a-z0-9][a-z0-9._+-]*)>=([0-9][^']*)'`)
	wrappedImageRevisionPattern = regexp.MustCompile(`^-[1-9][0-9]*$`)
	dindDockerfile              = filepath.Join("erun-devops", "docker", "erun-dind", "Dockerfile")
	runtimeImageDockerfile      = filepath.Join("erun-devops", "docker", "erun-devops", "Dockerfile")
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(erunRepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// A commented-out ARG is not a pin, so it neither counts nor hides a second one.
func pinnedDockerfileArg(t *testing.T, dockerfile, arg string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^\s*ARG\s+` + regexp.QuoteMeta(arg) + `=(\S+)`)
	matches := pattern.FindAllStringSubmatch(dockerfileWithoutComments(readRepoFile(t, dockerfile)), -1)
	if len(matches) != 1 {
		t.Fatalf("%s: want exactly one ARG %s, found %d; the pin moved somewhere this test does not read", dockerfile, arg, len(matches))
	}
	return matches[0][1]
}

func requirePinAtOrAbove(t *testing.T, where, pinned, floor, fixes string) {
	t.Helper()
	version, ok := leadingVersion(pinned)
	if !ok {
		t.Errorf("%s: cannot read a version from %q", where, pinned)
		return
	}
	if minimum, _ := leadingVersion(floor); versionBelow(version, minimum) {
		t.Errorf("%s pins %s, below %s, the first release that fixes %s", where, pinned, floor, fixes)
	}
}

// Reads sibling Dockerfiles and VERSION files: the build runs it with -count=1.
func TestWrappedServiceImagesPinPastTheirAdvisories(t *testing.T) {
	for _, entry := range wrappedImageSecurityFloors {
		dockerfile := filepath.Join("erun-devops", "docker", entry.image, "Dockerfile")
		pinned := pinnedDockerfileArg(t, dockerfile, entry.arg)
		requirePinAtOrAbove(t, dockerfile+" "+entry.arg, pinned, entry.floor, entry.fixes)
		version := strings.TrimSpace(readRepoFile(t, filepath.Join("erun-devops", "docker", entry.image, "VERSION")))
		revision, found := strings.CutPrefix(version, pinned)
		if !found || (revision != "" && !wrappedImageRevisionPattern.MatchString(revision)) {
			t.Errorf("erun-devops/docker/%s/VERSION is %s but its Dockerfile pins %s=%s: that tag would name an image built from another upstream release", entry.image, version, entry.arg, pinned)
		}
	}
}

// Every in-pod build runs the runtime image's docker CLI and buildx against the sidecar's daemon.
func TestRuntimeDockerToolchainMovesWithTheDindDaemon(t *testing.T) {
	cli := pinnedDockerfileArg(t, runtimeImageDockerfile, "DOCKER_VERSION")
	if daemon := pinnedDockerfileArg(t, dindDockerfile, "DOCKER_VERSION"); cli != daemon {
		t.Errorf("the runtime image's docker CLI is %s but the dind sidecar runs dockerd %s: move them together, since that CLI drives that daemon on every in-pod build", cli, daemon)
	}
	requirePinAtOrAbove(t, runtimeImageDockerfile+" DOCKER_VERSION", cli, dockerEngineSecurityFloor, "the Go 1.23.8 standard library advisories in the 28.1.1 CLI")
	buildx := pinnedDockerfileArg(t, runtimeImageDockerfile, "BUILDX_VERSION")
	requirePinAtOrAbove(t, runtimeImageDockerfile+" BUILDX_VERSION", buildx, buildxSecurityFloor, "the grpc, x/crypto, x/net, containerd, BuildKit and Go 1.22 standard library advisories in v0.18.0")
}

// apkVersion splits "1.2.1-r0" into its upstream version and its package revision.
func apkVersion(value string) ([3]int, int, bool) {
	upstream, revision, hasRevision := strings.Cut(value, "-r")
	version, ok := leadingVersion(upstream)
	if !ok || !hasRevision {
		return version, 0, ok
	}
	n, err := strconv.Atoi(revision)
	if err != nil {
		return [3]int{}, 0, false
	}
	return version, n, true
}

func apkVersionBelow(t *testing.T, pinned, floor string) bool {
	t.Helper()
	version, revision, ok := apkVersion(pinned)
	if !ok {
		t.Errorf("cannot read an apk version from %q", pinned)
		return false
	}
	minimum, minimumRevision, _ := apkVersion(floor)
	if version != minimum {
		return versionBelow(version, minimum)
	}
	return revision < minimumRevision
}

func TestDindApkFloorsHoldOnItsAlpineBranch(t *testing.T) {
	if pinned := pinnedDockerfileArg(t, dindDockerfile, "DOCKER_VERSION"); pinned != dindAlpineFloorsCheckedOn {
		t.Fatalf("%s pins docker %s but its apk floors were read from docker:%s-dind's Alpine branch: scan the new base, set each dindAlpinePackageFloors entry to the fixed package on its branch, then move dindAlpineFloorsCheckedOn", dindDockerfile, pinned, dindAlpineFloorsCheckedOn)
	}
	constraints := map[string]string{}
	for _, match := range apkFloorConstraintPattern.FindAllStringSubmatch(dockerfileWithoutComments(readRepoFile(t, dindDockerfile)), -1) {
		constraints[match[1]] = match[2]
	}
	if len(constraints) == 0 {
		t.Fatalf("%s: no 'package>=version' apk floor found; the floors moved somewhere this test does not read", dindDockerfile)
	}
	for name, floor := range dindAlpinePackageFloors {
		pinned, found := constraints[name]
		if !found {
			t.Errorf("%s no longer requires %s>=%s: apk would keep the upstream image's vulnerable package with nothing failing", dindDockerfile, name, floor)
			continue
		}
		if apkVersionBelow(t, pinned, floor) {
			t.Errorf("%s requires %s>=%s, below %s, the first %s on its Alpine branch past the advisories the upstream image carries", dindDockerfile, name, pinned, floor, name)
		}
	}
}

// docker 29 puts only a fresh data root on the containerd image store, so without this pin a
// new environment's docker-state volume stores images unlike every carried-over one.
func TestDindDaemonKeepsTheOverlay2ImageStore(t *testing.T) {
	rel := filepath.Join("erun-devops", "docker", "erun-dind", "daemon.json")
	var daemon struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, rel)), &daemon); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	if enabled, set := daemon.Features["containerd-snapshotter"]; !set || enabled {
		t.Errorf("%s must set features.containerd-snapshotter to false: docker 29 otherwise moves a fresh docker-state volume onto the containerd image store, which keeps each layer compressed and unpacked, while every existing volume stays on overlay2", rel)
	}
}
