package normalize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The step-timing table is rendered in real wall-clock order (erun-common's
// orderedTimingRows sorts by measured duration), so the only thing that makes
// it safe to store in a golden is canonicalizeStepTimingOrder reordering every
// level by name. A row shape the canonicalizer cannot parse ends the block
// early and silently leaves the rest of that tree in wall-clock order — green
// on an idle machine, red wherever the timings actually diverge.
//
// These tests assert the property directly rather than the pattern that
// implements it: for every golden that carries a timing block, canonicalizing
// a sibling-reordered copy must reproduce the golden byte for byte. The
// reordering is derived from indentation alone, deliberately independent of
// timingLinePattern, so this stays a check ON the pattern instead of a
// restatement of it.

func TestGoldenTimingBlocksAreOrderInvariant(t *testing.T) {
	goldens := goldensWithTimingBlocks(t)
	if len(goldens) == 0 {
		t.Fatal("no goldens carry a step-timing block; this gate would assert nothing")
	}
	for _, path := range goldens {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			golden := string(raw)
			if got := canonicalizeStepTimingOrder(golden); got != golden {
				t.Fatalf("golden is not stored in canonical timing order; canonicalizing it changes it:\nwant:\n%s\ngot:\n%s", golden, got)
			}
			shuffled := reverseTimingSiblings(golden)
			if shuffled == golden {
				t.Skip("timing block has no sibling pair to reorder")
			}
			if got := canonicalizeStepTimingOrder(shuffled); got != golden {
				t.Fatalf("canonicalization did not undo a sibling reorder, so this golden still depends on the order the run measured:\nwant:\n%s\ngot:\n%s", golden, got)
			}
		})
	}
}

// TestCanonicalizeStepTimingOrderIgnoresInterleavedOutput pins the property
// the canonicalizer exists to provide for a table that has unrelated output
// inside it: the canonical form must not depend on where that output landed.
//
// The block is the release scenario's own table, whose five rows are
// build -> publish -> api plus release and sync-remote beside publish. Two
// captures of exactly that table are built below, differing only in the order
// the run measured its children in and in which boundaries an unrelated
// writer's line landed on -- which is the difference a loaded machine
// produced against the recorded golden, and every landing used to split the
// table into fragments that were each sorted on their own.
//
// Unlike the golden-driven test above, this needs no golden to carry an
// interleaved line today: after a row's own multi-line message is rendered
// indented (erun-common/timing.go), no recorded table has one left, so a
// probe driven by the committed goldens would assert nothing at all while
// still reporting green.
func TestCanonicalizeStepTimingOrderIgnoresInterleavedOutput(t *testing.T) {
	// The recorded capture: children measured in name order, the writer's
	// lines landing after three of them.
	recorded := strings.Join([]string{
		timingBlockHeader,
		"  build (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"concurrent output",
		"    publish (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"concurrent output",
		"      api (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"concurrent output",
		"    release [<ELAPSED>]",
		"    sync-remote [<ELAPSED>]",
		"timing record written to <TMP>",
	}, "\n")

	// The same table off a loaded machine: build's children measured in the
	// other order, so the writer's lines land on different boundaries.
	loaded := strings.Join([]string{
		timingBlockHeader,
		"  build (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"concurrent output",
		"    sync-remote [<ELAPSED>]",
		"concurrent output",
		"    release [<ELAPSED>]",
		"    publish (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"concurrent output",
		"      api (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"timing record written to <TMP>",
	}, "\n")

	// The block the canonical form names: rows in name order at every level,
	// the unrelated lines together after them, before the footer.
	want := strings.Join([]string{
		timingBlockHeader,
		"  build (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"    publish (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"      api (failed) [<ELAPSED>] — docker buildx inspect: exit status 1",
		"    release [<ELAPSED>]",
		"    sync-remote [<ELAPSED>]",
		"concurrent output",
		"concurrent output",
		"concurrent output",
		"timing record written to <TMP>",
	}, "\n")

	for name, capture := range map[string]string{"recorded": recorded, "loaded": loaded} {
		if got := canonicalizeStepTimingOrder(capture); got != want {
			t.Errorf("canonicalizing the %s capture must produce the block's canonical form, so that two runs of one scenario cannot disagree:\nwant:\n%s\ngot:\n%s", name, want, got)
		}
	}
}

func goldensWithTimingBlocks(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..", "testdata")
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".txt") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), timingBlockHeader) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk testdata: %v", err)
	}
	return out
}

// reverseTimingSiblings rewrites every timing block in s with each level's
// children in reverse order — the cheapest reordering that is guaranteed to
// differ from the canonical one whenever more than one sibling exists. It
// nests rows by leading indentation only, so it agrees with the canonicalizer
// about the tree without borrowing its notion of what a row looks like.
func reverseTimingSiblings(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) != timingBlockHeader {
			out = append(out, lines[i])
			i++
			continue
		}
		out = append(out, lines[i])
		end := timingBlockEndIndentAware(lines, i+1)
		out = append(out, reverseIndentTree(lines[i+1:end])...)
		i = end
	}
	return strings.Join(out, "\n")
}

// timingBlockEndIndentAware returns the end of the table the header at
// headerIdx opens. It prefers the block's own footer -- the line
// reportStepTiming writes after the table -- and falls back to the
// indentation rule when there is none, matching where the canonicalizer stops
// reordering. Using indentation alone here would cut the probe off at the
// first interleaved stderr line, which is precisely the boundary the
// canonicalizer no longer draws, so the probe would skip asserting the
// property on exactly the golden shapes this file exists to check.
func timingBlockEndIndentAware(lines []string, bodyStart int) int {
	const footer = "timing record written to "
	for i := bodyStart; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], footer) {
			return i
		}
	}
	end := bodyStart
	for end < len(lines) && indentWidth(lines[end]) >= 2 {
		end++
	}
	return end
}

func indentWidth(line string) int {
	if strings.TrimSpace(line) == "" {
		return 0
	}
	return len(line) - len(strings.TrimLeft(line, " "))
}

type indentNode struct {
	lines    []string
	children []*indentNode
}

// reverseIndentTree parses an indentation-nested run of lines and re-serializes
// it with every level's children reversed. A row is a line carrying the
// redacted duration token; any line after it without one is part of that row's
// multi-line error message and stays attached to it. That is the whole of what
// this borrows from the production side — it never consults timingLinePattern,
// so a row shape the pattern stops recognizing still gets reordered here and
// the mismatch surfaces.
func reverseIndentTree(lines []string) []string {
	root := &indentNode{}
	stack := []*indentNode{root}
	depths := []int{-1}
	for _, line := range lines {
		if !strings.Contains(line, "[<ELAPSED>]") {
			top := stack[len(stack)-1]
			top.lines = append(top.lines, line)
			continue
		}
		depth := indentWidth(line)
		for len(stack) > 1 && depths[len(depths)-1] >= depth {
			stack = stack[:len(stack)-1]
			depths = depths[:len(depths)-1]
		}
		node := &indentNode{lines: []string{line}}
		stack[len(stack)-1].children = append(stack[len(stack)-1].children, node)
		stack = append(stack, node)
		depths = append(depths, depth)
	}
	// Lines the root accumulated sit before the first row, so they belong to
	// no node: emitting them first keeps them in the block rather than
	// dropping them, which would make every comparison below fail for a reason
	// that has nothing to do with the property under test.
	out := append([]string(nil), root.lines...)
	var walk func(*indentNode)
	walk = func(n *indentNode) {
		for i := len(n.children) - 1; i >= 0; i-- {
			child := n.children[i]
			out = append(out, child.lines...)
			walk(child)
		}
	}
	walk(root)
	return out
}
