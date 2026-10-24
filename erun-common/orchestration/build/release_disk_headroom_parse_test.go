package build

import (
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

func TestParseDFDiskBytes(t *testing.T) {
	t.Run("long filesystem name wraps onto its own line", func(t *testing.T) {
		output := "Filesystem                                                          1024-blocks     Used Available Capacity Mounted on\n" +
			"a-very-long-overlay-filesystem-identifier-that-wraps-the-data-row\n" +
			"                                                                       102400000 51200000  20971520      51% /var/lib/docker\n"
		free, total, ok := diskheadroom.ParseDFDiskBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(20971520) * 1024; free != want {
			t.Fatalf("free = %d, want %d", free, want)
		}
		// A wrapped row drops the filesystem field; total is found relative to Capacity.
		if want := uint64(102400000) * 1024; total != want {
			t.Fatalf("total = %d, want %d", total, want)
		}
	})

	t.Run("empty output is inconclusive", func(t *testing.T) {
		if _, _, ok := diskheadroom.ParseDFDiskBytes(""); ok {
			t.Fatal("expected ok=false for empty output")
		}
	})

	t.Run("malformed data row is inconclusive", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\nnot enough fields\n"
		if _, _, ok := diskheadroom.ParseDFDiskBytes(output); ok {
			t.Fatal("expected ok=false for a data row with too few fields")
		}
	})
}

func TestResolveMinDiskHeadroomBytes(t *testing.T) {
	const largeDisk uint64 = 435 << 30 // 435 GiB -> 43.5 GiB proportional

	t.Run("defaults to the absolute floor when total is unknown", func(t *testing.T) {
		t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, "")
		if got := diskheadroom.ResolveMinDiskHeadroomBytes(0); got != diskheadroom.ReleaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, diskheadroom.ReleaseMinDiskHeadroomBytes)
		}
	})

	t.Run("scales past the absolute floor on a large disk", func(t *testing.T) {
		t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, "")
		want := largeDisk / 100 * diskheadroom.MinDiskHeadroomPercent
		got := diskheadroom.ResolveMinDiskHeadroomBytes(largeDisk)
		if got != want {
			t.Fatalf("got %d, want proportional %d", got, want)
		}
		if got <= diskheadroom.ReleaseMinDiskHeadroomBytes {
			t.Fatalf("proportional floor %d should exceed the absolute floor %d", got, diskheadroom.ReleaseMinDiskHeadroomBytes)
		}
	})

	// kubelet's nodefs evictionHard defaults to 5%; a floor below it prunes only after eviction.
	t.Run("clears kubelet's 5 percent eviction threshold", func(t *testing.T) {
		t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, "")
		evictionThreshold := largeDisk / 100 * 5
		if got := diskheadroom.ResolveMinDiskHeadroomBytes(largeDisk); got <= evictionThreshold {
			t.Fatalf("floor %d must sit above the eviction threshold %d", got, evictionThreshold)
		}
	})

	t.Run("honors a valid override outright", func(t *testing.T) {
		t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, "1073741824")
		if got := diskheadroom.ResolveMinDiskHeadroomBytes(largeDisk); got != 1073741824 {
			t.Fatalf("got %d, want 1073741824", got)
		}
	})

	t.Run("falls back to the resolved floor on a malformed override", func(t *testing.T) {
		t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, "not-a-number")
		if got := diskheadroom.ResolveMinDiskHeadroomBytes(0); got != diskheadroom.ReleaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, diskheadroom.ReleaseMinDiskHeadroomBytes)
		}
	})
}

func TestParseBuildxDuReclaimable(t *testing.T) {
	const listing = "ID\t\t\t\t\t\tRECLAIMABLE\tSIZE\t\tLAST ACCESSED\n" +
		"2khmsvw4ntqfc7uq0q8r1y64c\t\ttrue \t\t0B        \t14 hours ago\n" +
		"2ke0dwuumo5alaz7lo0x6gtnm\t\ttrue \t\t0B*       \t30 hours ago\n" +
		"13tec09hlxprb0vpk1d0ayztv\t\ttrue \t\t120.4MB   \t2026-09-21 03:04:05 +0000 UTC\n" +
		"Shared:\t\t24.17GB\n" +
		"Private:\t19.2GB\n" +
		"Reclaimable:\t43.37GB\n" +
		"Total:\t\t43.37GB\n"

	cases := []struct {
		name string
		in   string
		want uint64
		ok   bool
	}{
		{"a listing with a reclaimable summary", listing, 43370000000, true},
		{"an empty cache reads as zero, not as unreadable", "Shared:\t0B\nPrivate:\t0B\nReclaimable:\t0B\nTotal:\t0B\n", 0, true},
		{"no summary at all is unreadable", "2khmsvw4ntqfc7uq0q8r1y64c\t\ttrue \t\t0B        \t14 hours ago\n", 0, false},
		{"empty output is unreadable", "", 0, false},
		{"a size the parser cannot read is unreadable", "Reclaimable:\tsome\n", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := diskheadroom.ParseBuildxDuReclaimable(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
