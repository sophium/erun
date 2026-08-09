package eruncommon

import (
	"slices"
	"strings"
	"testing"
)

// A release used to build every platform and only then push them all. On a
// daemon backed by the containerd image store that lost the first platform's
// manifest before its push ran — the content is collectable once nothing but a
// tag refers to it — so the release died with "content digest ... not found"
// after paying for every build.
//
// The contract that fixes it is an ordering one: a platform is published before
// the next platform is built, so no artifact has to survive another build. These
// tests pin that ordering, per the repository rule that every release failure
// mode gets a regression test.

func buildSpecForPushOrder(promote bool) DockerBuildSpec {
	return DockerBuildSpec{
		ContextDir:  "/src",
		Image:       DockerImageReference{Tag: "ghcr.io/acme/widget:1.2.3", Version: "1.2.3"},
		Platforms:   []string{"linux/amd64", "linux/arm64"},
		Push:        true,
		Promote:     promote,
		Fingerprint: "cafe",
	}
}

// indexOfCommand returns the position of the first traced command whose joined
// arguments contain every needle, or -1.
func indexOfCommand(commands []commandSpec, needles ...string) int {
	for i, command := range commands {
		joined := strings.Join(command.Args, " ")
		if slices.ContainsFunc(needles, func(n string) bool { return !strings.Contains(joined, n) }) {
			continue
		}
		return i
	}
	return -1
}

func TestEachPlatformIsPushedBeforeTheNextIsBuilt(t *testing.T) {
	commands := buildSpecForPushOrder(false).traceCommands()

	amd64Build := indexOfCommand(commands, "build", "linux/amd64")
	amd64Push := indexOfCommand(commands, "push", "1.2.3-amd64")
	arm64Build := indexOfCommand(commands, "build", "linux/arm64")
	arm64Push := indexOfCommand(commands, "push", "1.2.3-arm64")

	for name, idx := range map[string]int{
		"amd64 build": amd64Build, "amd64 push": amd64Push,
		"arm64 build": arm64Build, "arm64 push": arm64Push,
	} {
		if idx < 0 {
			t.Fatalf("%s missing from the traced commands: %+v", name, commands)
		}
	}

	if !(amd64Build < amd64Push) {
		t.Fatalf("a platform must be pushed after it is built, got build=%d push=%d", amd64Build, amd64Push)
	}
	// The whole point: nothing that was built may wait behind another build.
	if !(amd64Push < arm64Build) {
		t.Fatalf("amd64 must be published before arm64 starts building, or its content need not survive; got amd64 push=%d arm64 build=%d", amd64Push, arm64Build)
	}
	if !(arm64Build < arm64Push) {
		t.Fatalf("arm64 must be pushed after it is built, got build=%d push=%d", arm64Build, arm64Push)
	}
}

// The manifest list is assembled last and reads its inputs back from the
// registry, so it neither needs nor keeps the per-arch images locally.
func TestMultiArchManifestIsAssembledAfterEveryPlatformIsPushed(t *testing.T) {
	commands := buildSpecForPushOrder(false).traceCommands()

	lastPush := indexOfCommand(commands, "push", "1.2.3-arm64")
	create := indexOfCommand(commands, "manifest", "create")
	push := indexOfCommand(commands, "manifest", "push")

	if create < 0 || push < 0 {
		t.Fatalf("multi-arch assembly missing: %+v", commands)
	}
	if !(lastPush < create && create < push) {
		t.Fatalf("expected every platform pushed, then manifest create, then manifest push; got lastPush=%d create=%d push=%d", lastPush, create, push)
	}
}

// The promote path re-tags already-built fingerprint images rather than building
// them, but it publishes the same per-arch tags and so carries the same hazard.
func TestPromotePublishesEachPlatformBeforeTaggingTheNext(t *testing.T) {
	commands := buildSpecForPushOrder(true).traceCommands()

	amd64Push := indexOfCommand(commands, "push", "1.2.3-amd64")
	arm64Tag := indexOfCommand(commands, "tag", "1.2.3-arm64")

	if amd64Push < 0 || arm64Tag < 0 {
		t.Fatalf("promote trace missing a push or tag: %+v", commands)
	}
	if !(amd64Push < arm64Tag) {
		t.Fatalf("promote must publish amd64 before it moves on to arm64; got push=%d next tag=%d", amd64Push, arm64Tag)
	}
}

// A build that is not pushing must emit no push at all — the ordering fix must
// not turn a local build into a publish.
func TestALocalBuildStillPushesNothing(t *testing.T) {
	spec := buildSpecForPushOrder(false)
	spec.Push = false

	for _, command := range spec.traceCommands() {
		joined := strings.Join(command.Args, " ")
		if strings.HasPrefix(joined, "push ") || strings.Contains(joined, "manifest ") {
			t.Fatalf("a non-pushing build must not publish anything, got %q", joined)
		}
	}
}
