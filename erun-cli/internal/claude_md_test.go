package internal

import (
	"strings"
	"testing"
)

func TestUpsertAgentInstructionsBlockAppendsToEmpty(t *testing.T) {
	got := upsertAgentInstructionsBlock("")
	if got != claudeMDBlock {
		t.Fatalf("empty content should produce exactly the block, got:\n%q", got)
	}
}

func TestUpsertAgentInstructionsBlockAppendsAfterExistingContent(t *testing.T) {
	got := upsertAgentInstructionsBlock("# My notes\n")
	want := "# My notes\n\n" + claudeMDBlock
	if got != want {
		t.Fatalf("expected block appended after user content with one blank line, got:\n%q", got)
	}
}

func TestUpsertAgentInstructionsBlockIsIdempotent(t *testing.T) {
	for _, start := range []string{"", "# My notes\n", claudeMDBlock} {
		once := upsertAgentInstructionsBlock(start)
		twice := upsertAgentInstructionsBlock(once)
		if once != twice {
			t.Fatalf("upsert must be stable on its own output:\nstart:\n%q\nonce:\n%q\ntwice:\n%q", start, once, twice)
		}
	}
}

func TestUpsertAgentInstructionsBlockReplacesStaleBlock(t *testing.T) {
	stale := "# My notes\n\n" + claudeMDOpenMarker + "\n# Agent Instructions\n\nold guidance\n" + claudeMDCloseMarker + "\n"
	got := upsertAgentInstructionsBlock(stale)
	if strings.Contains(got, "old guidance") {
		t.Fatalf("stale block content must be replaced, got:\n%q", got)
	}
	if !strings.Contains(got, "read the source directly") {
		t.Fatalf("current block content must be present, got:\n%q", got)
	}
	if n := strings.Count(got, claudeMDOpenMarker); n != 1 {
		t.Fatalf("exactly one managed block expected, got %d:\n%q", n, got)
	}
	if !strings.HasPrefix(got, "# My notes\n") {
		t.Fatalf("user content must be preserved, got:\n%q", got)
	}
}

func TestUpsertAgentInstructionsBlockRecoversFromDanglingOpenMarker(t *testing.T) {
	dangling := "# My notes\n\n" + claudeMDOpenMarker + "\n# Agent Instructions\n(no close marker)\n"
	got := upsertAgentInstructionsBlock(dangling)
	if n := strings.Count(got, claudeMDOpenMarker); n != 1 {
		t.Fatalf("expected exactly one open marker, got %d:\n%q", n, got)
	}
	if n := strings.Count(got, claudeMDCloseMarker); n != 1 {
		t.Fatalf("expected exactly one close marker, got %d:\n%q", n, got)
	}
	if !strings.HasPrefix(got, "# My notes\n") {
		t.Fatalf("user content before the dangling marker must be preserved, got:\n%q", got)
	}
}
