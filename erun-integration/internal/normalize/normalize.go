// Package normalize replaces variable parts of erun output with stable
// placeholders so golden files stay deterministic across machines and runs.
package normalize

import (
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Replacement substitutes a regex match with Token. Rules run in order, so
// earlier rules consume input before later ones see it.
type Replacement struct {
	Pattern *regexp.Regexp
	Token   string
}

var defaultRules = []Replacement{
	// First, so later rules never see escape codes.
	{regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`), ""},
	// A minted MCP bearer signs a live timestamp; early, before rules chew on JWT
	// segments.
	{regexp.MustCompile(`"token": "[A-Za-z0-9_.\-]+"`), `"token": "<TOKEN>"`},
	// Before the version rule, which would otherwise eat "127.0.0".
	{regexp.MustCompile(`\b127\.0\.0\.1\b`), "<LOOPBACK>"},
	// The 'v' alternation: \b fails between a tag's 'v' and its digits.
	{regexp.MustCompile(`(?:\bv|\b)\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`), "<VERSION>"},
	// Go's zero instant gets its own token so a never-set timestamp stays visible
	// in goldens.
	{regexp.MustCompile(`\b0001-01-01T00:00:00(?:\.0+)?Z\b`), "<ZERO-TS>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<TS>"},
	// Lease remaining seconds and alive-beat age depend on wall clock.
	{regexp.MustCompile(`expires in \d+s`), "expires in <REMAINING>"},
	{regexp.MustCompile(`last beat \d+ms ago`), "last beat <ALIVE_AGE>ms ago"},
	{regexp.MustCompile(`\b20\d{12}\b`), "<TS_COMPACT>"},
	// Terraform work dir under the test HOME; before <TMP> so goldens still show
	// state lives outside the project tree.
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*?/\.erun/terraform`), "<STATE_ROOT>"},
	// os.UserConfigDir differs by OS and darwin's contains a space the generic
	// <TMP> rule stops at, so collapse the whole path first.
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*(?:/Library/Application Support)?/ERun/desktopid\.key`), "<DESKTOP_IDENTITY>"},
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*(?:/Library/Application Support)?/ERun/desktopid\.pub`), "<DESKTOP_IDENTITY_PUBLIC>"},
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*(?:/Library/Application Support)?/ERun/desktop-control\.json`), "<TMP>"},
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]+`), "<TMP>"},
	// Percent-escaped temp paths.
	{regexp.MustCompile(`%2F(?:private%2F)?(?:var%2Ffolders|var%2Ftmp|tmp)%2F[A-Za-z0-9._%+-]*`), "<TMP>"},
	// Windows temp roots, mirroring the Unix rules; inert on Unix.
	{regexp.MustCompile(`(?i)[A-Za-z]:[\\/](?:[^\\/\s'"]+[\\/])*?temp[\\/][^\s'"]*?[\\/]\.erun[\\/]terraform`), "<STATE_ROOT>"},
	{regexp.MustCompile(`(?i)[A-Za-z]:[\\/](?:[^\\/\s'"]+[\\/])*?temp[\\/][^\s'"]*`), "<TMP>"},
	// WSL2 mount view of a Windows temp dir; before the /Users and /home rules.
	{regexp.MustCompile(`(?i)/mnt/[a-z]/(?:[^/\s'"]+/)*?temp/[^\s'"]*`), "<TMP>"},
	// Windows home; before the Unix /Users rule.
	{regexp.MustCompile(`(?i)[A-Za-z]:[\\/]Users[\\/][^\\/\s'"]+`), "<HOME>"},
	// Also collapses deterministic UUIDs (e.g. JetBrains ssh config IDs).
	{regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<UUID>"},
	// The "in "/"after " prefix keeps timeout literals like "2m0s" intact.
	{regexp.MustCompile(` (in|after) \d+(?:[hm]\d+)*s\b`), " $1 <ELAPSED>"},
	{regexp.MustCompile(`elapsed: \d+(?:\.\d+)?[a-zµμ]+(?:\d+(?:\.\d+)?[a-zµμ]+)*\b`), "elapsed: <ELAPSED>"},
	// Step-timing durations are bracketed so only they are redacted.
	{regexp.MustCompile(`\[\d+(?:\.\d+)?(?:ms|µs|us|ns|h|m|s)(?:\d+(?:\.\d+)?(?:ms|µs|us|ns|h|m|s))*\]`), "[<ELAPSED>]"},
	// A synthetic timing row's presence depends on wall clock, so drop the whole
	// line. Must run after the bracket-duration rule.
	{regexp.MustCompile(`(?m)^[ ]*\((?:unaccounted|ran concurrently, overlap)\) \[<ELAPSED>\]\n`), ""},
	// The hash covers per-test temp paths.
	{regexp.MustCompile(`hash=[0-9a-f]{16}\b`), "hash=<HASH>"},
	{regexp.MustCompile(`pid=\d+`), "pid=<PID>"},
	{regexp.MustCompile(`"pid": \d+`), `"pid": <PID>`},
	{regexp.MustCompile(`\bpid \d+`), "pid <PID>"},
	{regexp.MustCompile(`\(PID \d+\)`), "(PID <PID>)"},
	{regexp.MustCompile(`\bprocess group \d+`), "process group <PGID>"},
	{regexp.MustCompile(`\b(supervisor|process) \d+ is gone`), "$1 <PID> is gone"},
	{regexp.MustCompile(`id=[A-Za-z0-9_./-]*-\d{10,}\b`), "id=<COMMAND_ID>"},
	{regexp.MustCompile(`-[0-9a-f]{16}\b`), "-<HEX16>"},
	{regexp.MustCompile(`\b[0-9a-f]{32,}\b`), "<HEX>"},
	{regexp.MustCompile(`\bcommit = [0-9a-f]{7,12}\b`), "commit = <SHORTSHA>"},
	{regexp.MustCompile(`\bcheckpointed as [0-9a-f]{7,12}\b`), "checkpointed as <SHORTSHA>"},
	// Real git output. The range rule is anchored to push status lines so a diff's
	// stable `index <blob>..<blob>` survives.
	{regexp.MustCompile(`\[([^\s\]]+) [0-9a-f]{7,40}\]`), "[$1 <SHORTSHA>]"},
	{regexp.MustCompile(`(?m)^(\s+)[0-9a-f]{7,40}\.\.[0-9a-f]{7,40}\b`), "${1}<SHORTSHA>..<SHORTSHA>"},
	{regexp.MustCompile(`(?m)^Updating [0-9a-f]{7,40}\.\.[0-9a-f]{7,40}\b`), "Updating <SHORTSHA>..<SHORTSHA>"},
	// Forced ref updates print a three-dot range; `git tag -f` prints "(was <sha>)".
	{regexp.MustCompile(`(?m)^(\s*\+ )[0-9a-f]{7,40}\.\.\.[0-9a-f]{7,40}\b`), "${1}<SHORTSHA>...<SHORTSHA>"},
	{regexp.MustCompile(`\(was [0-9a-f]{7,40}\)`), "(was <SHORTSHA>)"},
	// Safety net for a real home path leaking past the test HOME override.
	{regexp.MustCompile(`/Users/[^/\s'"]+`), "<HOME>"},
	{regexp.MustCompile(`/home/[^/\s'"]+`), "<HOME>"},
	// Strip .exe only from a launched erun binary at line start; .exe elsewhere is
	// literal data (e.g. Scoop manifests) that must survive.
	{regexp.MustCompile(`(?m)^('?)(erun-app|erun|emcp|eapi)\.exe\b`), "${1}${2}"},
	// Missing-binary exec errors differ per OS.
	{regexp.MustCompile(`(?:fork/exec|exec:)[^\n]*<TMP>[^\n]*`), "<EXEC_ERROR>"},
	// Keyed by the machine's hostname.
	{regexp.MustCompile(`/docker/erun-build-cpu-cap-\S+`), "/docker/erun-build-cpu-cap-<POD>"},
	{regexp.MustCompile(`[ \t]+\n`), "\n"},
}

// windowsDrivePath matches drive-letter Windows paths only; bare backslashes are
// far more often escapes than separators.
var windowsDrivePath = regexp.MustCompile(`[A-Za-z]:\\[^\s'"]*`)

// PromptConfirm is Apply plus collapsing promptui "[Y/n]" repaint frames to the
// settled render. It assumes the confirm owns its output stream; a racing
// writer corrupts frames beyond recognition.
func PromptConfirm(s string) string {
	s = applyBackspaces(strings.ReplaceAll(Apply(s), "\r", ""))
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, "█") || strings.Contains(line, "[Y/n]") {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == line {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// Apply runs the default rules, then extra.
func Apply(s string, extra ...Replacement) string {
	// Before the token rules, which match forward-slash paths.
	if runtime.GOOS == "windows" {
		s = forwardSlashWindowsPaths(s)
	}
	rules := append([]Replacement(nil), defaultRules...)
	rules = append(rules, extra...)
	for _, r := range rules {
		s = r.Pattern.ReplaceAllString(s, r.Token)
	}
	// Sibling order is the last wall-clock-derived signal in a timing block.
	s = canonicalizeStepTimingOrder(s)
	s = strings.TrimRight(s, "\n") + "\n"
	return s
}

// timingLinePattern matches a redacted step-timing row: even indentation, label,
// "[<ELAPSED>]", optional failure suffix. TestGoldenTimingBlocksAreOrderInvariant
// guards new row shapes.
var timingLinePattern = regexp.MustCompile(`^((?: )*)\S.* \[<ELAPSED>\](?: .*)?$`)

// canonicalizeStepTimingOrder sorts every level of each step-timing tree by
// name, since subprocess wall-clock times can swap sibling order between runs.
// A block spans header to footer, so interleaved child stderr cannot split it.
func canonicalizeStepTimingOrder(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == timingBlockHeader {
			if body := timingBlockBodyEnd(lines, i+1); body >= 0 {
				rows, foreign := splitTimingBlockBody(lines[i+1 : body])
				out = append(out, lines[i])
				out = append(out, sortedTimingForest(rows)...)
				// Foreign lines go after the table: their position is a scheduling artifact.
				out = append(out, foreign...)
				out = append(out, lines[body])
				i = body + 1
				continue
			}
			// No footer: fall back to the per-run rule rather than running to EOF.
		}
		if _, ok := timingLineDepth(lines[i]); ok {
			end := timingRunEnd(lines, i)
			out = append(out, sortedTimingForest(lines[i:end])...)
			i = end
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return strings.Join(out, "\n")
}

const timingBlockHeader = "step timing (ordered by duration):"

const timingBlockFooter = "timing record written to "

// timingBlockBodyEnd returns the footer index after bodyStart, or -1.
func timingBlockBodyEnd(lines []string, bodyStart int) int {
	for i := bodyStart; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], timingBlockFooter) {
			return i
		}
	}
	return -1
}

// timingRunEnd returns the end of the run of timing rows and continuation lines
// at start; used when there is no header or footer.
func timingRunEnd(lines []string, start int) int {
	end := start
	for end < len(lines) {
		if _, ok := timingLineDepth(lines[end]); ok {
			end++
			continue
		}
		if !timingContinuationLine(lines[end]) {
			break
		}
		end++
	}
	return end
}

// splitTimingBlockBody separates a block body into row lines (rows plus
// indented continuations) and unindented foreign output.
func splitTimingBlockBody(body []string) (rows, foreign []string) {
	for _, line := range body {
		if _, ok := timingLineDepth(line); ok {
			rows = append(rows, line)
			continue
		}
		if timingContinuationLine(line) {
			rows = append(rows, line)
			continue
		}
		foreign = append(foreign, line)
	}
	return rows, foreign
}

// timingLineDepth reports a row's relative nesting depth, and whether line is a
// timing row.
func timingLineDepth(line string) (int, bool) {
	m := timingLinePattern.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	return len(m[1]) / 2, true
}

// timingContinuationLine reports whether line continues a row's multi-line
// error; every table line is indented, the footer is not.
func timingContinuationLine(line string) bool {
	return strings.HasPrefix(line, "  ")
}

// timingNode is a parsed timing row; extra holds its continuation lines.
type timingNode struct {
	line     string
	depth    int
	extra    []string
	children []*timingNode
}

// sortedTimingForest parses rows into a tree by depth and re-serializes it with
// every level sorted by name.
func sortedTimingForest(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	var roots []*timingNode
	var stack []*timingNode
	for _, line := range lines {
		depth, ok := timingLineDepth(line)
		if !ok {
			// A continuation line travels with the current row.
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.extra = append(top.extra, line)
			}
			continue
		}
		node := &timingNode{line: line, depth: depth}
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, node)
		}
		stack = append(stack, node)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return strings.TrimSpace(roots[i].line) < strings.TrimSpace(roots[j].line)
	})
	out := make([]string, 0, len(lines))
	for _, root := range roots {
		sortTimingNodeChildren(root)
		appendTimingNode(&out, root)
	}
	return out
}

func sortTimingNodeChildren(node *timingNode) {
	sort.SliceStable(node.children, func(i, j int) bool {
		return strings.TrimSpace(node.children[i].line) < strings.TrimSpace(node.children[j].line)
	})
	for _, child := range node.children {
		sortTimingNodeChildren(child)
	}
}

func appendTimingNode(out *[]string, node *timingNode) {
	*out = append(*out, node.line)
	*out = append(*out, node.extra...)
	for _, child := range node.children {
		appendTimingNode(out, child)
	}
}

// forwardSlashWindowsPaths forward-slashes drive-letter paths so Unix-shaped
// token rules match.
func forwardSlashWindowsPaths(s string) string {
	return windowsDrivePath.ReplaceAllStringFunc(s, func(m string) string {
		m = strings.ReplaceAll(m, `\`, "/")
		// %q-quoted paths arrive with doubled separators.
		return multiSlash.ReplaceAllString(m, "/")
	})
}

var multiSlash = regexp.MustCompile(`/{2,}`)

var backspacePair = regexp.MustCompile("[^\n\x08]\x08")

// applyBackspaces resolves \x08 like a terminal; Windows readline emits erase
// sequences the unix build does not.
func applyBackspaces(s string) string {
	if !strings.ContainsRune(s, '\x08') {
		return s
	}
	for {
		next := backspacePair.ReplaceAllString(s, "")
		if next == s {
			return strings.ReplaceAll(next, "\x08", "")
		}
		s = next
	}
}
