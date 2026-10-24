package build

import (
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

func TestParseDFDiskBytes(t *testing.T) {
	t.Run("standard one-line output", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"/dev/sda1        102400000 51200000  41943040      56% /var/lib/docker\n"
		free, total, ok := shared.ParseDFDiskBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(41943040) * 1024; free != want {
			t.Fatalf("free = %d, want %d", free, want)
		}
		if want := uint64(102400000) * 1024; total != want {
			t.Fatalf("total = %d, want %d", total, want)
		}
	})

	t.Run("long filesystem name wraps onto its own line", func(t *testing.T) {
		output := "Filesystem                                                          1024-blocks     Used Available Capacity Mounted on\n" +
			"a-very-long-overlay-filesystem-identifier-that-wraps-the-data-row\n" +
			"                                                                       102400000 51200000  20971520      51% /var/lib/docker\n"
		free, total, ok := shared.ParseDFDiskBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(20971520) * 1024; free != want {
			t.Fatalf("free = %d, want %d", free, want)
		}
		// The wrapped row drops the filesystem field, shifting every column
		// one left — the total still has to be found relative to Capacity.
		if want := uint64(102400000) * 1024; total != want {
			t.Fatalf("total = %d, want %d", total, want)
		}
	})

	t.Run("empty output is inconclusive", func(t *testing.T) {
		if _, _, ok := shared.ParseDFDiskBytes(""); ok {
			t.Fatal("expected ok=false for empty output")
		}
	})

	t.Run("malformed data row is inconclusive", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\nnot enough fields\n"
		if _, _, ok := shared.ParseDFDiskBytes(output); ok {
			t.Fatal("expected ok=false for a data row with too few fields")
		}
	})
}

func TestResolveMinDiskHeadroomBytes(t *testing.T) {
	// A filesystem small enough that 10% of it is under the absolute floor.
	const smallDisk uint64 = 100 << 30 // 100 GiB -> 10 GiB proportional
	// A filesystem big enough that 10% of it clears the absolute floor — the
	// shape of the node that evicted every pod while free space still sat
	// above a 20 GiB floor.
	const largeDisk uint64 = 435 << 30 // 435 GiB -> 43.5 GiB proportional

	t.Run("defaults to the absolute floor when total is unknown", func(t *testing.T) {
		t.Setenv(shared.ReleaseMinDiskHeadroomEnv, "")
		if got := shared.ResolveMinDiskHeadroomBytes(0); got != shared.ReleaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, shared.ReleaseMinDiskHeadroomBytes)
		}
	})

	t.Run("keeps the absolute floor when it exceeds the proportional one", func(t *testing.T) {
		t.Setenv(shared.ReleaseMinDiskHeadroomEnv, "")
		if got := shared.ResolveMinDiskHeadroomBytes(smallDisk); got != shared.ReleaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want the absolute floor %d", got, shared.ReleaseMinDiskHeadroomBytes)
		}
	})

	t.Run("scales past the absolute floor on a large disk", func(t *testing.T) {
		t.Setenv(shared.ReleaseMinDiskHeadroomEnv, "")
		want := largeDisk / 100 * shared.MinDiskHeadroomPercent
		got := shared.ResolveMinDiskHeadroomBytes(largeDisk)
		if got != want {
			t.Fatalf("got %d, want proportional %d", got, want)
		}
		if got <= shared.ReleaseMinDiskHeadroomBytes {
			t.Fatalf("proportional floor %d should exceed the absolute floor %d", got, shared.ReleaseMinDiskHeadroomBytes)
		}
	})

	// The whole point of the proportional floor: kubelet's evictionHard for
	// nodefs defaults to 5% available, so a floor at or below that line can
	// only prune after eviction has already begun.
	t.Run("clears kubelet's 5 percent eviction threshold", func(t *testing.T) {
		t.Setenv(shared.ReleaseMinDiskHeadroomEnv, "")
		evictionThreshold := largeDisk / 100 * 5
		if got := shared.ResolveMinDiskHeadroomBytes(largeDisk); got <= evictionThreshold {
			t.Fatalf("floor %d must sit above the eviction threshold %d", got, evictionThreshold)
		}
	})

	t.Run("honors a valid override outright", func(t *testing.T) {
		t.Setenv(shared.ReleaseMinDiskHeadroomEnv, "1073741824")
		if got := shared.ResolveMinDiskHeadroomBytes(largeDisk); got != 1073741824 {
			t.Fatalf("got %d, want 1073741824", got)
		}
	})

	t.Run("falls back to the resolved floor on a malformed override", func(t *testing.T) {
		t.Setenv(shared.ReleaseMinDiskHeadroomEnv, "not-a-number")
		if got := shared.ResolveMinDiskHeadroomBytes(0); got != shared.ReleaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, shared.ReleaseMinDiskHeadroomBytes)
		}
	})
}

func TestFormatGiB(t *testing.T) {
	if got, want := shared.FormatGiB(20<<30), "20.0 GiB"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseBuildxDuReclaimable(t *testing.T) {
	// The shape `docker buildx du` actually writes: a per-record table whose
	// last column is a timestamp, closed by a colon-keyed summary block.
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
		// A zero the check treats as "nothing to reclaim" is still an answer,
		// not a failed read — the two lead to different decisions.
		{"an empty cache reads as zero, not as unreadable", "Shared:\t0B\nPrivate:\t0B\nReclaimable:\t0B\nTotal:\t0B\n", 0, true},
		{"no summary at all is unreadable", "2khmsvw4ntqfc7uq0q8r1y64c\t\ttrue \t\t0B        \t14 hours ago\n", 0, false},
		{"empty output is unreadable", "", 0, false},
		{"a size the parser cannot read is unreadable", "Reclaimable:\tsome\n", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := shared.ParseBuildxDuReclaimable(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseDockerSize(t *testing.T) {
	// Shapes taken from real `docker system df --format` output on a build box.
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"8.914GB", 8914000000, true},
		{"0B", 0, true},
		{"250.8MB", 250800000, true},
		{"103.4GB", 103400000000, true},
		{"512kB", 512000, true},
		// Some rows carry a trailing percentage; the number is still the size.
		{"77.29GB (100%)", 77290000000, true},
		{"", 0, false},
		{"not-a-size", 0, false},
	}
	for _, tc := range cases {
		got, ok := shared.ParseDockerSize(tc.in)
		if ok != tc.ok {
			t.Errorf("parseDockerSize(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
