// Package normalize replaces variable parts of erun output (paths, versions,
// timestamps, port numbers, hashes) with stable placeholders so golden files
// stay deterministic across machines and runs.
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
	// Strip ANSI escape sequences before anything else so subsequent rules
	// don't have to account for them.
	{regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`), ""},
	// Build version: 1.0.51-snapshot-20260508025226 or 1.0.51 or 1.0.51-rc.1
	{regexp.MustCompile(`\b\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`), "<VERSION>"},
	// ISO 8601 timestamps with or without zone.
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<TS>"},
	// Compact timestamps used in snapshot labels (YYYYMMDDHHMMSS).
	{regexp.MustCompile(`\b20\d{12}\b`), "<TS_COMPACT>"},
	// Temp dir paths the OS hands out (Linux/macOS).
	{regexp.MustCompile(`/(?:var/)?(?:tmp|private/var/folders)/[^\s'"]+`), "<TMP>"},
	// Random hex tokens used for chart names (e.g., -f0bb16f86125afa9).
	{regexp.MustCompile(`-[0-9a-f]{16}\b`), "-<HEX16>"},
	// Random hex tokens of other lengths embedded in identifiers.
	{regexp.MustCompile(`\b[0-9a-f]{32,}\b`), "<HEX>"},
	// User home dir prefix when it leaks despite HOME override.
	{regexp.MustCompile(`/Users/[^/\s'"]+`), "<HOME>"},
	{regexp.MustCompile(`/home/[^/\s'"]+`), "<HOME>"},
	// Trailing whitespace on each line.
	{regexp.MustCompile(`[ \t]+\n`), "\n"},
}

// Apply runs the default rule set over the given output. Use this for the
// majority of scenarios; the optional extra rules are appended after the
// defaults so they can override or further normalize the result.
func Apply(s string, extra ...Replacement) string {
	rules := append([]Replacement(nil), defaultRules...)
	rules = append(rules, extra...)
	for _, r := range rules {
		s = r.Pattern.ReplaceAllString(s, r.Token)
	}
	// Collapse trailing newlines so one or more become exactly one. Stable
	// across "did the last line end with newline" jitter.
	s = strings.TrimRight(s, "\n") + "\n"
	return s
}
