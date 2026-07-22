// Package normalize replaces variable parts of erun output with stable
// placeholders so golden files stay deterministic across machines and runs.
package normalize

import (
	"regexp"
	"runtime"
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
	// The Terraform durable work dir (local-backend state + TF_DATA_DIR) lives
	// under the test HOME temp dir. Collapse just its temp-path prefix to a stable
	// token — before the generic <TMP> rule below would swallow the whole path — so
	// goldens still show that state lives off the (project <TMP>) playbook tree.
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*?/\.erun/terraform`), "<STATE_ROOT>"},
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]+`), "<TMP>"},
	// A separate rule for temp paths whose separators are percent-escaped,
	// which the plain-path rule above cannot match.
	{regexp.MustCompile(`%2F(?:private%2F)?(?:var%2Ffolders|var%2Ftmp|tmp)%2F[A-Za-z0-9._%+-]*`), "<TMP>"},
	// Windows path roots (either separator): t.TempDir() lands under
	// <drive>:\Users\<user>\AppData\Local\Temp\<test>. Mirror the Unix rules —
	// STATE_ROOT terraform variant first, then the generic temp dir — so a golden
	// recorded on either OS normalizes to the same token. Inert on Unix output
	// (no drive letter), so existing macOS/Linux goldens are unaffected.
	{regexp.MustCompile(`(?i)[A-Za-z]:[\\/](?:[^\\/\s'"]+[\\/])*?temp[\\/][^\s'"]*?[\\/]\.erun[\\/]terraform`), "<STATE_ROOT>"},
	{regexp.MustCompile(`(?i)[A-Za-z]:[\\/](?:[^\\/\s'"]+[\\/])*?temp[\\/][^\s'"]*`), "<TMP>"},
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
	// Windows home dir (after the temp rules, so temp paths under it collapsed
	// first). Inert on Unix output.
	{regexp.MustCompile(`(?i)[A-Za-z]:[\\/]Users[\\/][^\\/\s'"]+`), "<HOME>"},
	{regexp.MustCompile(`[ \t]+\n`), "\n"},
}

// tokenRootedPath matches a path rooted at a normalized token so its tail's
// separators can be canonicalized; the token itself carries no separator, so
// backslashes elsewhere in the output are left untouched.
var tokenRootedPath = regexp.MustCompile(`(?:<TMP>|<HOME>|<STATE_ROOT>)(?:[\\/][^\s'"\\/]+)+`)

// shellSafeQuotedArg matches a single-quoted argument whose content is entirely
// shell-safe (only the chars traceShellQuote leaves unquoted, plus <TOKEN>s).
// Such an arg is never quoted on Unix — only Windows quotes it, because the raw
// path had backslashes before normalization — so stripping the quotes makes the
// two platforms render the same argv. A quoted arg with any other char (JSON,
// spaces) is quoted on both OSes and is left alone.
var shellSafeQuotedArg = regexp.MustCompile(`'([A-Za-z0-9/._:=+<>-]+)'`)

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
	// Windows-only: the OS-native output uses backslash separators and quotes
	// path args the Unix tracer leaves bare. Canonicalize to the Unix shape so a
	// golden recorded on either OS matches. Gated on GOOS so macOS/Linux output —
	// already canonical — is provably untouched.
	if runtime.GOOS == "windows" {
		s = canonicalizeWindowsPaths(s)
	}
	// Absorb the jitter in whether the final line ends with a newline.
	s = strings.TrimRight(s, "\n") + "\n"
	return s
}

// canonicalizeWindowsPaths rewrites Windows path shapes to the Unix form the
// goldens are recorded in: forward-slash separators within token-rooted paths,
// then dropping the quotes traceShellQuote added around a now-shell-safe path
// arg. Exported-free helper so the cross-OS unit test can drive it directly.
func canonicalizeWindowsPaths(s string) string {
	s = tokenRootedPath.ReplaceAllStringFunc(s, func(m string) string {
		return strings.ReplaceAll(m, `\`, "/")
	})
	return shellSafeQuotedArg.ReplaceAllString(s, "$1")
}
