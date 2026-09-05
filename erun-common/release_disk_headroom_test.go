package eruncommon

import "testing"

func TestParseDFAvailableBytes(t *testing.T) {
	t.Run("standard one-line output", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"/dev/sda1        102400000 51200000  41943040      56% /var/lib/docker\n"
		got, ok := parseDFAvailableBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(41943040) * 1024; got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("long filesystem name wraps onto its own line", func(t *testing.T) {
		output := "Filesystem                                                          1024-blocks     Used Available Capacity Mounted on\n" +
			"a-very-long-overlay-filesystem-identifier-that-wraps-the-data-row\n" +
			"                                                                       102400000 51200000  20971520      51% /var/lib/docker\n"
		got, ok := parseDFAvailableBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(20971520) * 1024; got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("empty output is inconclusive", func(t *testing.T) {
		if _, ok := parseDFAvailableBytes(""); ok {
			t.Fatal("expected ok=false for empty output")
		}
	})

	t.Run("malformed data row is inconclusive", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\nnot enough fields\n"
		if _, ok := parseDFAvailableBytes(output); ok {
			t.Fatal("expected ok=false for a data row with too few fields")
		}
	})
}

func TestResolveReleaseMinDiskHeadroomBytes(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		if got := resolveReleaseMinDiskHeadroomBytes(); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	t.Run("honors a valid override", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "1073741824")
		if got := resolveReleaseMinDiskHeadroomBytes(); got != 1073741824 {
			t.Fatalf("got %d, want 1073741824", got)
		}
	})

	t.Run("falls back to the default on a malformed override", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "not-a-number")
		if got := resolveReleaseMinDiskHeadroomBytes(); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})
}

func TestFormatGiB(t *testing.T) {
	if got, want := formatGiB(20<<30), "20.0 GiB"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
