package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

// A merge queue gates the prospective squash merge it composes. When the source
// branch was rebased onto the target tip first, that merge's tree is
// byte-identical to the branch tip's, so a content-only fingerprint makes the
// gate build promote the image the READY build produced minutes earlier and the
// test stage the gate exists to run never executes. These tests lock in the
// gate build's identity being bound to the commit it gates.

const gatedTestCommit = "3c21ac82c0ffee1234567890abcdef0123456789"

func newGatedCommitFingerprintSpec(root string) DockerBuildSpec {
	contextDir := filepath.Join(root, "mod", "docker", "comp")
	return DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: filepath.Join(contextDir, "Dockerfile"),
		Image:          DockerImageReference{ImageName: "comp", Tag: "comp"},
		Platforms:      []string{"linux/amd64"},
	}
}

func writeGatedCommitFingerprintContext(t *testing.T, root string) {
	t.Helper()
	spec := newGatedCommitFingerprintSpec(root)
	if err := os.MkdirAll(spec.ContextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec.DockerfilePath, []byte("FROM scratch\nCOPY app /app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec.ContextDir, "app"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gatedCommitFingerprint(t *testing.T, spec DockerBuildSpec) string {
	t.Helper()
	digest, err := computeBuildFingerprint(spec)
	if err != nil {
		t.Fatalf("computeBuildFingerprint: %v", err)
	}
	return digest
}

// TestBuildFingerprintBindsAGateBuildToTheCommitItGates is the defect
// reproduction: the READY build's image was produced from a different commit
// than the prospective merge, and only the gated commit distinguishes them.
func TestBuildFingerprintBindsAGateBuildToTheCommitItGates(t *testing.T) {
	root := t.TempDir()
	writeGatedCommitFingerprintContext(t, root)

	ordinary := newGatedCommitFingerprintSpec(root)
	ordinaryFingerprint := gatedCommitFingerprint(t, ordinary)

	gated := newGatedCommitFingerprintSpec(root)
	gated.GatedCommit = gatedTestCommit
	gatedFingerprint := gatedCommitFingerprint(t, gated)

	if gatedFingerprint == ordinaryFingerprint {
		t.Fatal("expected a gate build's fingerprint to differ from the content-only fingerprint of the same tree")
	}

	otherCommit := newGatedCommitFingerprintSpec(root)
	otherCommit.GatedCommit = "ee2cb4021234567890abcdef0123456789abcdef"
	if otherFingerprint := gatedCommitFingerprint(t, otherCommit); otherFingerprint == gatedFingerprint {
		t.Fatal("expected fingerprints of different gated commits to differ")
	}

	// A retry of the same gate must still promote the image that gate attempt
	// already built, or every retry would pay a full rebuild.
	retry := newGatedCommitFingerprintSpec(root)
	retry.GatedCommit = gatedTestCommit
	if retryFingerprint := gatedCommitFingerprint(t, retry); retryFingerprint != gatedFingerprint {
		t.Fatal("expected the same gated commit to reproduce the same fingerprint")
	}
}

// TestApplyIncrementalPromotionNeverPromotesAGateBuildFromAnotherCommitsImage
// holds the invariant directly: a locally tagged image whose fingerprint is the
// content identity of a tree built from a different commit must not satisfy a
// gate build, while an image built for the gated commit itself still does.
func TestApplyIncrementalPromotionNeverPromotesAGateBuildFromAnotherCommitsImage(t *testing.T) {
	root := t.TempDir()
	writeGatedCommitFingerprintContext(t, root)

	ordinary := newGatedCommitFingerprintSpec(root)
	contentFingerprint := gatedCommitFingerprint(t, ordinary)

	gated := newGatedCommitFingerprintSpec(root)
	gated.GatedCommit = gatedTestCommit
	gatedFingerprintValue := gatedCommitFingerprint(t, gated)

	// The local daemon holds only the READY build's image: content identity, no
	// gated commit.
	present := map[string]bool{
		fingerprintTag(ordinary.Image, contentFingerprint, "linux/amd64"): true,
	}
	inspect := func(tag string) (bool, error) { return present[tag], nil }

	out, err := applyIncrementalPromotion([]DockerBuildSpec{gated}, inspect)
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if out[0].Promote {
		t.Fatal("a gate build promoted a cached image built from a different commit")
	}
	if len(out[0].MissingFingerprintPlatforms) == 0 {
		t.Fatal("expected the gate build to report the missing fingerprint platform that forced a rebuild")
	}

	// The image that gate attempt itself produced does satisfy it.
	present[fingerprintTag(gated.Image, gatedFingerprintValue, "linux/amd64")] = true
	out, err = applyIncrementalPromotion([]DockerBuildSpec{gated}, inspect)
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if !out[0].Promote {
		t.Fatal("expected a re-gate of the same merge commit to promote its own image")
	}
}

// TestApplyIncrementalPromotionStillPromotesOrdinaryBuildsAcrossCommits guards
// the cost side of the fix: ordinary builds keep their content-only identity, so
// a commit that leaves the build context untouched still promotes instead of
// paying a full rebuild.
func TestApplyIncrementalPromotionStillPromotesOrdinaryBuildsAcrossCommits(t *testing.T) {
	root := t.TempDir()
	writeGatedCommitFingerprintContext(t, root)

	ordinary := newGatedCommitFingerprintSpec(root)
	contentFingerprint := gatedCommitFingerprint(t, ordinary)

	inspect := func(tag string) (bool, error) {
		return tag == fingerprintTag(ordinary.Image, contentFingerprint, "linux/amd64"), nil
	}
	out, err := applyIncrementalPromotion([]DockerBuildSpec{ordinary}, inspect)
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if !out[0].Promote {
		t.Fatal("expected an ordinary build with an unchanged context to still promote")
	}
}
