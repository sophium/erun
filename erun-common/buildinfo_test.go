package eruncommon

import "testing"

func TestNormalizeBuildInfoDefaultsVersion(t *testing.T) {
	got := NormalizeBuildInfo(BuildInfo{})
	if got.Version != "dev" || got.Commit != "" || got.Date != "" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestFormatVersionLine(t *testing.T) {
	got := FormatVersionLine(BuildInfo{
		Version: "1.2.3",
		Commit:  "abcdef",
		Date:    "2024-01-01",
	})
	if got != "erun 1.2.3 (abcdef built 2024-01-01)" {
		t.Fatalf("unexpected formatted version: %q", got)
	}
}

func TestFormatVersionLineWithoutTail(t *testing.T) {
	got := FormatVersionLine(BuildInfo{Version: "1.2.3"})
	if got != "erun 1.2.3" {
		t.Fatalf("unexpected formatted version: %q", got)
	}
}
