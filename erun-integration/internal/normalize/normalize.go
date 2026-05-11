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
	// Loopback IP — emit a stable token before the version rule so its
	// dotted-numeric form isn't trimmed (RE2 has no negative lookahead, so
	// the version regex would otherwise eat "127.0.0" and leave a dangling
	// ".1" suffix in the golden).
	{regexp.MustCompile(`\b127\.0\.0\.1\b`), "<LOOPBACK>"},
	// Build version: 1.0.51-snapshot-20260508025226 or 1.0.51 or 1.0.51-rc.1.
	// Also match a leading 'v' prefix common in git tags
	// (v1.4.2-rc.7ad18f4) — without the alternation the \b would refuse to
	// match when 'v' is a word char immediately before '1'.
	{regexp.MustCompile(`(?:\bv|\b)\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`), "<VERSION>"},
	// ISO 8601 timestamps with or without zone.
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<TS>"},
	// Compact timestamps used in snapshot labels (YYYYMMDDHHMMSS).
	{regexp.MustCompile(`\b20\d{12}\b`), "<TS_COMPACT>"},
	// Temp dir paths the OS hands out (Linux/macOS).
	// Matches /tmp/..., /var/tmp/..., /var/folders/... (macOS $TMPDIR), and
	// the /private-prefixed variants on macOS.
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]+`), "<TMP>"},
	// Elapsed durations after deploy/build progress markers
	// ("in 0s", "after 1m23s"). The timeout literal "2m0s" is kept because
	// it lacks the leading "in "/"after " context.
	{regexp.MustCompile(` (in|after) \d+(?:[hm]\d+)*s\b`), " $1 <ELAPSED>"},
	// `elapsed: 1ms` / `elapsed: 12.3s` / `elapsed: 1m4s` from --time
	// feedback. Numeric run with optional fractional component + unit
	// suffix (Go time.Duration formatting).
	{regexp.MustCompile(`elapsed: \d+(?:\.\d+)?[a-zµμ]+(?:\d+(?:\.\d+)?[a-zµμ]+)*\b`), "elapsed: <ELAPSED>"},
	// Random base64url tokens used by cloud_context.newCloudContextToken
	// (32 bytes -> 43-char base64url) and similar. Match a 30+ length
	// base64url run after `--token ` so we don't catch unrelated text.
	{regexp.MustCompile(`--token [A-Za-z0-9_-]{30,}`), "--token <TOKEN>"},
	// Deploy single-flight params hash (e.g., hash=80b663e6beea3955).
	// Computed from the helm command + chart path + values file path, all of
	// which include per-test temp dirs, so the raw hex differs across runs.
	{regexp.MustCompile(`hash=[0-9a-f]{16}\b`), "hash=<HASH>"},
	// Test-runner process id surfaced by dedup claim/replay trace lines
	// (e.g., pid=96018). These lines became visible in real-run goldens
	// once decisions/inputs/outputs were lifted to the default Info
	// verbosity; without normalization the PID drifts every run.
	{regexp.MustCompile(`pid=\d+`), "pid=<PID>"},
	// Running-command marker id (e.g., id=build-1778402661251293000 or
	// id=deploy-team-dev-team-devops-1778402661251293000). The trailing
	// nanosecond timestamp varies per invocation so normalize the id token.
	{regexp.MustCompile(`id=[A-Za-z0-9_./-]*-\d{10,}\b`), "id=<COMMAND_ID>"},
	// Random hex tokens used for chart names (e.g., -f0bb16f86125afa9).
	{regexp.MustCompile(`-[0-9a-f]{16}\b`), "-<HEX16>"},
	// Random hex tokens of other lengths embedded in identifiers.
	{regexp.MustCompile(`\b[0-9a-f]{32,}\b`), "<HEX>"},
	// Git short commit SHAs (7-12 hex chars after `commit = ` or as
	// standalone tokens in tag names like `v1.4.2-rc.7ad18f4`).
	{regexp.MustCompile(`\bcommit = [0-9a-f]{7,12}\b`), "commit = <SHORTSHA>"},
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
