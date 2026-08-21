package provision

import "testing"

// TestReleaseJobParams pins the placement the release Job runs with: the
// tenant's own <registry>/<tenant>-devops:<runtimeVersion> runtime image.
func TestReleaseJobParams(t *testing.T) {
	params := releaseJobParams(
		ReleaseConfig{Registry: "ghcr.io/sophium", RuntimeVersion: "1.0.185", Namespace: "acme-agents", ServiceAccount: "acme-release"},
		ReleaseInput{Tenant: "acme", TargetBranch: "main", CommitID: "abc123", ReleaseID: "rel-1", Attempt: 1},
	)
	if params.Image != "ghcr.io/sophium/acme-devops:1.0.185" {
		t.Fatalf("image = %q, want ghcr.io/sophium/acme-devops:1.0.185", params.Image)
	}
}

// TestReleaseJobParamsBootstrap locks the release-queue runner's image
// fallback: a tenant confirmed to have never published its own image
// (Bootstrap, set by resolveBootstrapImage before the durable workflow runs)
// gets a release Job that runs the canonical erun-devops image instead of one
// that can only ImagePullBackOff on an image nobody ever pushed.
func TestReleaseJobParamsBootstrap(t *testing.T) {
	params := releaseJobParams(
		ReleaseConfig{Registry: "ghcr.io/sophium", RuntimeVersion: "1.0.185", Namespace: "acme-agents", ServiceAccount: "acme-release"},
		ReleaseInput{Tenant: "operations", TargetBranch: "main", CommitID: "abc123", ReleaseID: "rel-1", Attempt: 1, Bootstrap: true},
	)
	if params.Image != "ghcr.io/sophium/erun-devops:1.0.185" {
		t.Fatalf("image = %q, want the canonical ghcr.io/sophium/erun-devops:1.0.185", params.Image)
	}
}

// TestReleaseQueueResolveBootstrapImage locks the synchronous precondition
// start runs before enqueueing the durable workflow, mirroring
// TestResolveBootstrapImage for the deploy Job.
func TestReleaseQueueResolveBootstrapImage(t *testing.T) {
	config := ReleaseConfig{Registry: "ghcr.io/sophium", RuntimeVersion: "1.0.185"}

	t.Run("nil checker never bootstraps", func(t *testing.T) {
		q := &ReleaseQueue{config: config}
		if q.resolveBootstrapImage("acme") {
			t.Fatal("resolveBootstrapImage = true, want false with no checker wired")
		}
	})

	t.Run("confirmed missing selects bootstrap", func(t *testing.T) {
		checker := &stubImageChecker{missing: true}
		q := &ReleaseQueue{config: config, imageChecker: checker}
		if !q.resolveBootstrapImage("operations") {
			t.Fatal("resolveBootstrapImage = false, want true on a confirmed-missing tenant image")
		}
		if checker.gotImage != "ghcr.io/sophium/operations-devops:1.0.185" {
			t.Fatalf("checker probed %q", checker.gotImage)
		}
	})

	t.Run("confirmed present does not bootstrap", func(t *testing.T) {
		q := &ReleaseQueue{config: config, imageChecker: &stubImageChecker{missing: false}}
		if q.resolveBootstrapImage("acme") {
			t.Fatal("resolveBootstrapImage = true, want false when the tenant's own image exists")
		}
	})
}
