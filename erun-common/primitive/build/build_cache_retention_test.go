package build

import (
	"strconv"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// buildCacheTestVolume is the docker volume the chart declares by default, in
// bytes: 50 GiB. The marks derived from it are asserted rather than restated,
// so a change to the shares cannot pass by moving both sides together.
const buildCacheTestVolume uint64 = 50 << 30

func buildCacheTestBounds() shared.BuildCacheBounds {
	return shared.ResolveBuildCacheBounds(buildCacheTestVolume)
}

func TestResolveBuildCacheBounds(t *testing.T) {
	bounds := buildCacheTestBounds()

	t.Run("the ceiling leaves a fifth of the volume to what a cache prune cannot reach", func(t *testing.T) {
		if want := uint64(40 << 30); bounds.Ceiling != want {
			t.Fatalf("ceiling = %d, want %d (80%% of a 50 GiB volume)", bounds.Ceiling, want)
		}
	})

	// A warning at the threshold it warns about is a status line printed after
	// the decision, not a warning: the whole value of it is arriving while the
	// remedy is still ahead rather than already taken.
	t.Run("the warning sits strictly below the ceiling", func(t *testing.T) {
		if want := uint64(35 << 30); bounds.Warning != want {
			t.Fatalf("warning = %d, want %d (70%% of a 50 GiB volume)", bounds.Warning, want)
		}
		if bounds.Warning >= bounds.Ceiling {
			t.Fatalf("warning %d must sit below ceiling %d", bounds.Warning, bounds.Ceiling)
		}
	})

	t.Run("scales with the volume rather than being a fixed size", func(t *testing.T) {
		// The same environment on a volume four times the size is allowed four
		// times the cache; a fixed ceiling would be far too tight for one and far
		// too loose for the other.
		double := shared.ResolveBuildCacheBounds(buildCacheTestVolume * 4)
		if double.Ceiling != bounds.Ceiling*4 {
			t.Fatalf("ceiling = %d, want %d", double.Ceiling, bounds.Ceiling*4)
		}
	})
}

func TestDockerVolumeBytes(t *testing.T) {
	t.Run("reads the declared volume size", func(t *testing.T) {
		t.Setenv(shared.DockerVolumeBytesEnv, strconv.FormatUint(buildCacheTestVolume, 10))
		got, err := shared.DockerVolumeBytes()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != buildCacheTestVolume {
			t.Fatalf("got %d, want %d", got, buildCacheTestVolume)
		}
	})

	// Absent is not zero. An environment with no docker volume has no cache to
	// bound, and a zero-byte volume would put every cache over its ceiling.
	t.Run("an absent declaration is an error, not a zero-byte volume", func(t *testing.T) {
		t.Setenv(shared.DockerVolumeBytesEnv, "")
		if _, err := shared.DockerVolumeBytes(); err == nil {
			t.Fatal("expected an unset volume size to be reported as unknown")
		}
	})

	t.Run("a malformed declaration is not a size to reclaim against", func(t *testing.T) {
		for _, raw := range []string{"not-a-number", "0", "-1", "50Gi"} {
			t.Setenv(shared.DockerVolumeBytesEnv, raw)
			if _, err := shared.DockerVolumeBytes(); err == nil {
				t.Fatalf("expected %q to be rejected", raw)
			}
		}
	})
}
