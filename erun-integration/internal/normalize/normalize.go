// Package normalize replaces variable parts of erun output with stable
// placeholders so golden files stay deterministic across machines and runs.
package normalize

import (
	"regexp"
	"strings"
)

// Replacement substitutes a regex match with a fixed token. Order matters
// because earlier rules consume input before later ones see it.
type Replacement struct {
	Pattern *regexp.Regexp
	Token   string
}

var defaultRules = []Replacement{
	// First, so later rules never see escape codes.
	{regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`), ""},
	// Loopback IP — emit a stable token before the version rule so its
	// dotted-numeric form isn't trimmed (RE2 has no negative lookahead, so
	// the version regex would otherwise eat "127.0.0" and leave a dangling
	// ".1" suffix in the golden).
	{regexp.MustCompile(`\b127\.0\.0\.1\b`), "<LOOPBACK>"},
	// The leading-'v' alternation is required: a bare \b refuses to match when
	// a git tag's 'v' is the word char immediately before the digits.
	{regexp.MustCompile(`(?:\bv|\b)\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`), "<VERSION>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<TS>"},
	{regexp.MustCompile(`\b20\d{12}\b`), "<TS_COMPACT>"},
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]+`), "<TMP>"},
	// A separate rule for temp paths whose separators are percent-escaped,
	// which the plain-path rule above cannot match.
	{regexp.MustCompile(`%2F(?:private%2F)?(?:var%2Ffolders|var%2Ftmp|tmp)%2F[A-Za-z0-9._%+-]*`), "<TMP>"},
	// Also collapses deterministic UUIDs (e.g. JetBrains ssh config IDs) on
	// purpose; both shapes are opaque tokens, so no coverage signal is lost.
	{regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<UUID>"},
	// Requires the leading "in "/"after " so the timeout literal "2m0s" stays
	// stable in goldens instead of being swept up as elapsed time.
	{regexp.MustCompile(` (in|after) \d+(?:[hm]\d+)*s\b`), " $1 <ELAPSED>"},
	{regexp.MustCompile(`elapsed: \d+(?:\.\d+)?[a-zµμ]+(?:\d+(?:\.\d+)?[a-zµμ]+)*\b`), "elapsed: <ELAPSED>"},
	// Anchored to `--token ` so a base64url-shaped run elsewhere in the output
	// is not swept up.
	{regexp.MustCompile(`--token [A-Za-z0-9_-]{30,}`), "--token <TOKEN>"},
	// The hash covers per-test temp-dir paths, so it differs across runs
	// despite looking like a stable digest.
	{regexp.MustCompile(`hash=[0-9a-f]{16}\b`), "hash=<HASH>"},
	{regexp.MustCompile(`pid=\d+`), "pid=<PID>"},
	{regexp.MustCompile(`id=[A-Za-z0-9_./-]*-\d{10,}\b`), "id=<COMMAND_ID>"},
	{regexp.MustCompile(`-[0-9a-f]{16}\b`), "-<HEX16>"},
	{regexp.MustCompile(`\b[0-9a-f]{32,}\b`), "<HEX>"},
	{regexp.MustCompile(`\bcommit = [0-9a-f]{7,12}\b`), "commit = <SHORTSHA>"},
	// Safety net for a real home path that leaks despite the test HOME override.
	{regexp.MustCompile(`/Users/[^/\s'"]+`), "<HOME>"},
	{regexp.MustCompile(`/home/[^/\s'"]+`), "<HOME>"},
	{regexp.MustCompile(`[ \t]+\n`), "\n"},
}

// PromptConfirm normalizes output containing a promptui "[Y/n]" confirm.
// readline repaints the confirm line an unpredictable number of times (and,
// under the slower coverage-instrumented binary, leaks partial fragments), so
// the prompt render is not a stable contract; it drops the repaint frames and
// keeps the deterministic remainder. It runs the default rules first, so use it
// in place of Apply, not alongside it.
func PromptConfirm(s string) string {
	s = strings.ReplaceAll(Apply(s), "\r", "")
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

// Apply runs the default rule set over the output. Extra rules run after the
// defaults, so a caller can override or further normalize the result.
func Apply(s string, extra ...Replacement) string {
	rules := append([]Replacement(nil), defaultRules...)
	rules = append(rules, extra...)
	for _, r := range rules {
		s = r.Pattern.ReplaceAllString(s, r.Token)
	}
	// Absorb the jitter in whether the final line ends with a newline.
	s = strings.TrimRight(s, "\n") + "\n"
	return s
}
