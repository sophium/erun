package normalize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenTimingBlocksAreOrderInvariant: every golden timing block survives a
// sibling reorder built from indentation alone, independent of timingLinePattern.
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

// TestCanonicalizeStepTimingOrderIgnoresInterleavedOutput: two captures that
// differ in child order and where unrelated output landed canonicalize alike.
func TestCanonicalizeStepTimingOrderIgnoresInterleavedOutput(t *testing.T) {
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

// reverseTimingSiblings reverses each level's children in every timing block,
// nesting by indentation only.
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

// timingBlockEndIndentAware returns the footer index, else the end of the
// indented run, matching where the canonicalizer stops.
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

// reverseIndentTree reverses every level's children; a line without
// "[<ELAPSED>]" stays with the row above. It never consults timingLinePattern.
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
	// Lines before the first row belong to no node; keep them first.
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
