package run

import (
	"strconv"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

// buildCacheTestVolume is the chart's default docker volume: 50 GiB.
const buildCacheTestVolume uint64 = 50 << 30

func buildCacheTestBounds() diskheadroom.BuildCacheBounds {
	return diskheadroom.ResolveBuildCacheBounds(buildCacheTestVolume)
}

func TestResolveBuildCacheBounds(t *testing.T) {
	bounds := buildCacheTestBounds()

	t.Run("the ceiling leaves a fifth of the volume to what a cache prune cannot reach", func(t *testing.T) {
		if want := uint64(40 << 30); bounds.Ceiling != want {
			t.Fatalf("ceiling = %d, want %d (80%% of a 50 GiB volume)", bounds.Ceiling, want)
		}
	})

	t.Run("the warning sits strictly below the ceiling", func(t *testing.T) {
		if want := uint64(35 << 30); bounds.Warning != want {
			t.Fatalf("warning = %d, want %d (70%% of a 50 GiB volume)", bounds.Warning, want)
		}
		if bounds.Warning >= bounds.Ceiling {
			t.Fatalf("warning %d must sit below ceiling %d", bounds.Warning, bounds.Ceiling)
		}
	})

	t.Run("scales with the volume rather than being a fixed size", func(t *testing.T) {
		double := diskheadroom.ResolveBuildCacheBounds(buildCacheTestVolume * 4)
		if double.Ceiling != bounds.Ceiling*4 {
			t.Fatalf("ceiling = %d, want %d", double.Ceiling, bounds.Ceiling*4)
		}
	})
}

func TestDockerVolumeBytes(t *testing.T) {
	t.Run("reads the declared volume size", func(t *testing.T) {
		t.Setenv(diskheadroom.DockerVolumeBytesEnv, strconv.FormatUint(buildCacheTestVolume, 10))
		got, err := diskheadroom.DockerVolumeBytes()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != buildCacheTestVolume {
			t.Fatalf("got %d, want %d", got, buildCacheTestVolume)
		}
	})

	t.Run("an absent declaration is an error, not a zero-byte volume", func(t *testing.T) {
		t.Setenv(diskheadroom.DockerVolumeBytesEnv, "")
		if _, err := diskheadroom.DockerVolumeBytes(); err == nil {
			t.Fatal("expected an unset volume size to be reported as unknown")
		}
	})

	t.Run("a malformed declaration is not a size to reclaim against", func(t *testing.T) {
		for _, raw := range []string{"not-a-number", "0", "-1", "50Gi"} {
			t.Setenv(diskheadroom.DockerVolumeBytesEnv, raw)
			if _, err := diskheadroom.DockerVolumeBytes(); err == nil {
				t.Fatalf("expected %q to be rejected", raw)
			}
		}
	})
}
