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

// timingBlockHeader is the line reportStepTiming prints above the table.
const timingBlockHeader = "step timing (ordered by duration):"

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
		end := i + 1
		for end < len(lines) && indentWidth(lines[end]) >= 2 {
			end++
		}
		out = append(out, reverseIndentTree(lines[i+1:end])...)
		i = end
	}
	return strings.Join(out, "\n")
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
	var out []string
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
