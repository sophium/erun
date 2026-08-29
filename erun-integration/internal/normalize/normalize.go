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
	// A minted MCP bearer signs a live timestamp, so its token can never be
	// stable in a golden; the claims are asserted from the parsed payload
	// instead. Early, so no later rule chews on a JWT segment.
	{regexp.MustCompile(`"token": "[A-Za-z0-9_.\-]+"`), `"token": "<TOKEN>"`},
	// Loopback IP — emit a stable token before the version rule so its
	// dotted-numeric form isn't trimmed (RE2 has no negative lookahead, so
	// the version regex would otherwise eat "127.0.0" and leave a dangling
	// ".1" suffix in the golden).
	{regexp.MustCompile(`\b127\.0\.0\.1\b`), "<LOOPBACK>"},
	// The leading-'v' alternation is required: a bare \b refuses to match when
	// a git tag's 'v' is the word char immediately before the digits.
	{regexp.MustCompile(`(?:\bv|\b)\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`), "<VERSION>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<TS>"},
	// An activity lease's remaining seconds are measured against the wall clock
	// at print time, so they land a second either side of the ttl depending on
	// how long the subprocess took to start.
	{regexp.MustCompile(`expires in \d+s`), "expires in <REMAINING>"},
	// A job's alive-beat age is milliseconds since the supervisor's last ~1s
	// heartbeat, measured against the wall clock at print time — inherently as
	// variable as the remaining-lease seconds above.
	{regexp.MustCompile(`last beat \d+ms ago`), "last beat <ALIVE_AGE>ms ago"},
	{regexp.MustCompile(`\b20\d{12}\b`), "<TS_COMPACT>"},
	// The Terraform durable work dir (local-backend state + TF_DATA_DIR) lives
	// under the test HOME temp dir. Collapse just its temp-path prefix to a stable
	// token — before the generic <TMP> rule below would swallow the whole path — so
	// goldens still show that state lives off the (project <TMP>) playbook tree.
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*?/\.erun/terraform`), "<STATE_ROOT>"},
	// The desktop identity resolves through os.UserConfigDir(), which no test
	// seam can pin: darwin puts it under "Library/Application Support" and Linux
	// under the XDG dir. The generic rule below stops at the space, so the same
	// scenario left a different remainder on each OS and its golden could only be
	// green on whichever one recorded it. Collapse the whole path first.
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*(?:/Library/Application Support)?/ERun/desktopid\.key`), "<DESKTOP_IDENTITY>"},
	// Same reasoning for the public half, which a deploy refusal names as the key
	// to re-supply. Scenarios that must prove the concrete path reached the
	// message assert it against the un-normalized capture.
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|var/tmp|tmp)/[^\s'"]*(?:/Library/Application Support)?/ERun/desktopid\.pub`), "<DESKTOP_IDENTITY_PUBLIC>"},
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
	// A local-agent env's host worktree renders through the WSL2 mount view of a
	// Windows temp dir (C:\...\Temp\... -> /mnt/<drive>/.../Temp/...). Collapse it
	// here, before the /Users and /home rules below would carve the drive prefix
	// off. Inert on macOS/Linux output, which carries no /mnt/<drive> worktree.
	{regexp.MustCompile(`(?i)/mnt/[a-z]/(?:[^/\s'"]+/)*?temp/[^\s'"]*`), "<TMP>"},
	// Windows home dir. Before the Unix /Users rule below, so C:/Users/<user>
	// (after backslash canonicalization) collapses whole rather than leaving the
	// drive prefix. Inert on Unix output.
	{regexp.MustCompile(`(?i)[A-Za-z]:[\\/]Users[\\/][^\\/\s'"]+`), "<HOME>"},
	// Also collapses deterministic UUIDs (e.g. JetBrains ssh config IDs) on
	// purpose; both shapes are opaque tokens, so no coverage signal is lost.
	{regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<UUID>"},
	// Requires the leading "in "/"after " so the timeout literal "2m0s" stays
	// stable in goldens instead of being swept up as elapsed time.
	{regexp.MustCompile(` (in|after) \d+(?:[hm]\d+)*s\b`), " $1 <ELAPSED>"},
	{regexp.MustCompile(`elapsed: \d+(?:\.\d+)?[a-zµμ]+(?:\d+(?:\.\d+)?[a-zµμ]+)*\b`), "elapsed: <ELAPSED>"},
	// The step-timing table (timing.go) wraps every duration in brackets —
	// `<label> [<duration>]` — specifically so this rule can redact real,
	// sub-second wall-clock measurements without also sweeping up an unrelated
	// bare "5m" in help text or a --rollout-timeout example.
	{regexp.MustCompile(`\[\d+(?:\.\d+)?(?:ms|µs|us|ns|h|m|s)(?:\d+(?:\.\d+)?(?:ms|µs|us|ns|h|m|s))*\]`), "[<ELAPSED>]"},
	// A step-timing "(unaccounted)"/"(ran concurrently, overlap)" row's very
	// presence — not just its duration — is decided by wall clock: it appears
	// only when a parent step's own time and its children's summed time differ
	// by at least timingOrderNoiseFloor (100ms), and a stubbed subprocess in a
	// real-run scenario costs a different amount of that 100ms budget on every
	// host. Run after the bracket-duration rule above so it matches the
	// already-normalized "[<ELAPSED>]", and drop the whole line (indentation
	// through the trailing newline) rather than just the duration, since a
	// present-here-absent-there row would still fail the comparison otherwise.
	{regexp.MustCompile(`(?m)^[ ]*\((?:unaccounted|ran concurrently, overlap)\) \[<ELAPSED>\]\n`), ""},
	// Anchored to `--token ` so a base64url-shaped run elsewhere in the output
	// is not swept up.
	{regexp.MustCompile(`--token [A-Za-z0-9_-]{30,}`), "--token <TOKEN>"},
	// The hash covers per-test temp-dir paths, so it differs across runs
	// despite looking like a stable digest.
	{regexp.MustCompile(`hash=[0-9a-f]{16}\b`), "hash=<HASH>"},
	{regexp.MustCompile(`pid=\d+`), "pid=<PID>"},
	// A job reports the process it is reconciled against and the group a cancel
	// signals. Both are real OS pids, so they differ on every run; the contract a
	// golden locks is that the job names a recorded process at all, never which
	// number the kernel handed out.
	{regexp.MustCompile(`\bpid \d+`), "pid <PID>"},
	// The port-forward adopt/replace traces name the process holding a local
	// port. A scenario that wants erun to actually stop that holder has to
	// present its real PID — production kills what the probe names — so the
	// number is whatever the kernel handed out on this run. Which process it
	// was is asserted from the rewritten state file, not from the trace.
	{regexp.MustCompile(`\(PID \d+\)`), "(PID <PID>)"},
	{regexp.MustCompile(`\bprocess group \d+`), "process group <PGID>"},
	{regexp.MustCompile(`\b(supervisor|process) \d+ is gone`), "$1 <PID> is gone"},
	{regexp.MustCompile(`id=[A-Za-z0-9_./-]*-\d{10,}\b`), "id=<COMMAND_ID>"},
	{regexp.MustCompile(`-[0-9a-f]{16}\b`), "-<HEX16>"},
	{regexp.MustCompile(`\b[0-9a-f]{32,}\b`), "<HEX>"},
	{regexp.MustCompile(`\bcommit = [0-9a-f]{7,12}\b`), "commit = <SHORTSHA>"},
	// A job's dirty-working-tree checkpoint names the real commit it made,
	// which is content-derived and differs per run of the same fixture repo.
	{regexp.MustCompile(`\bcheckpointed as [0-9a-f]{7,12}\b`), "checkpointed as <SHORTSHA>"},
	// Real git output in a real-run scenario: `git commit` prints
	// `[<branch> <sha>]` and `git push` prints an indented `<old>..<new>` range
	// per ref. Both carry the commit the fixture repo happened to produce on
	// this run. The range rule is anchored to the start of a push status line so
	// a diff's `index <blob>..<blob>` — content-derived and stable — survives.
	{regexp.MustCompile(`\[([^\s\]]+) [0-9a-f]{7,40}\]`), "[$1 <SHORTSHA>]"},
	{regexp.MustCompile(`(?m)^(\s+)[0-9a-f]{7,40}\.\.[0-9a-f]{7,40}\b`), "${1}<SHORTSHA>..<SHORTSHA>"},
	// A forced ref update (`git push --force`/`--force-with-lease`, as the
	// release tag's repoint-after-rebase does) prints a three-dot range, not
	// the fast-forward push status line's two-dot range above, and
	// `git tag -f` separately prints the commit it moved the tag away from.
	// Both carry a commit the fixture repo happened to produce on this run.
	{regexp.MustCompile(`(?m)^(\s*\+ )[0-9a-f]{7,40}\.\.\.[0-9a-f]{7,40}\b`), "${1}<SHORTSHA>...<SHORTSHA>"},
	{regexp.MustCompile(`\(was [0-9a-f]{7,40}\)`), "(was <SHORTSHA>)"},
	// Safety net for a real home path that leaks despite the test HOME override.
	{regexp.MustCompile(`/Users/[^/\s'"]+`), "<HOME>"},
	{regexp.MustCompile(`/home/[^/\s'"]+`), "<HOME>"},
	// Drop the Windows executable suffix from a launched erun binary so the trace
	// matches the Unix golden (erun-app.exe -> erun-app). Anchored to line start
	// (optionally after the command-quote a -vv trace adds) because that is where
	// erun prints the executable it invokes — resolveAppExecutable is the only
	// place erun appends a host .exe. A ".exe" anywhere else on a line is literal
	// data that must survive: a Scoop manifest's `"erun.exe"` bin entries and its
	// `\erun.exe` build path, or a `bin must include erun-app.exe` validation
	// message, are identical on every host and are not host-suffix noise. Inert on
	// Unix output, which has no .exe.
	{regexp.MustCompile(`(?m)^('?)(erun-app|erun|emcp|eapi)\.exe\b`), "${1}${2}"},
	// A failed exec of a missing path reads differently per platform — Unix
	// `fork/exec <TMP> no such file or directory`, Windows `exec: "<TMP>":
	// executable file not found in %PATH%` — so collapse either whole message to
	// one token (the path is already <TMP>) for an OS-invariant golden.
	{regexp.MustCompile(`(?:fork/exec|exec:)[^\n]*<TMP>[^\n]*`), "<EXEC_ERROR>"},
	{regexp.MustCompile(`[ \t]+\n`), "\n"},
}

// windowsDrivePath matches a drive-letter-rooted Windows path (C:\...), the only
// unambiguous Windows path shape: it starts with a drive letter and colon, so it
// can never match a shell escape sequence (\n, \033, \,) that also uses
// backslashes. Its separators are forward-slashed for golden parity. Paths
// without a drive letter (a Linux path erun mangled via filepath) are fixed at
// the source instead — the normalizer must not touch bare backslashes, which are
// escapes far more often than separators.
var windowsDrivePath = regexp.MustCompile(`[A-Za-z]:\\[^\s'"]*`)

// PromptConfirm normalizes output containing a promptui "[Y/n]" confirm.
// readline repaints the confirm line an unpredictable number of times, so the
// prompt render is not a stable contract; this drops every repaint frame that
// still shows the [Y/n] prompt or the █ cursor and dedups the rest, leaving the
// one settled render (label plus the typed answer) and the deterministic
// remainder. Runs the default rules first, so use it in place of Apply, not
// alongside it.
//
// This relies on the confirm owning its output stream: a second writer racing
// the same stream (as the open alias flow's stdout eval-script write once did)
// interleaves mid-redraw and leaves marker-less corrupted fragments this cannot
// recognize. Prompts that share a stream with other output must route the
// confirm elsewhere at the source (see cmd.confirmPromptTo) rather than lean on
// this to reconstruct the race.
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

// Apply runs the default rule set over the output. Extra rules run after the
// defaults, so a caller can override or further normalize the result.
func Apply(s string, extra ...Replacement) string {
	// Windows-only: forward-slash path separators BEFORE the token rules so a
	// path like `\home\erun\git\team` becomes `/home/erun/git/team` and the
	// /home, /Users, temp rules can collapse it to a token. Gated on GOOS so
	// macOS/Linux output — already canonical — is provably untouched.
	if runtime.GOOS == "windows" {
		s = forwardSlashWindowsPaths(s)
	}
	rules := append([]Replacement(nil), defaultRules...)
	rules = append(rules, extra...)
	for _, r := range rules {
		s = r.Pattern.ReplaceAllString(s, r.Token)
	}
	// Absorb the jitter in whether the final line ends with a newline.
	s = strings.TrimRight(s, "\n") + "\n"
	return s
}

// forwardSlashWindowsPaths rewrites backslash separators in drive-letter Windows
// paths (C:\...) to forward slashes so the downstream token rules — recorded in
// the Unix goldens' forward-slash shape — match. It deliberately touches only
// drive-rooted paths: a bare backslash is an escape sequence (\n, \033, \,) in a
// traced script body far more often than a separator, and quoting parity is
// handled at the tracer instead (isShellSafe treats backslash as safe, matching
// Unix's bare forward-slash args). Exported-free so the cross-OS unit test can
// drive it on any host.
func forwardSlashWindowsPaths(s string) string {
	return windowsDrivePath.ReplaceAllStringFunc(s, func(m string) string {
		m = strings.ReplaceAll(m, `\`, "/")
		// A %q-quoted path arrives with escaped separators (C:\\Users\\...), which
		// become // here; collapse runs to a single slash so the path matches the
		// Unix golden. Safe within a drive path — it contains no :// URL.
		return multiSlash.ReplaceAllString(m, "/")
	})
}

var multiSlash = regexp.MustCompile(`/{2,}`)

// backspacePair matches a printable character immediately followed by a
// backspace, the byte pair a terminal renders as "erase the previous glyph".
var backspacePair = regexp.MustCompile("[^\n\x08]\x08")

// applyBackspaces resolves ASCII backspaces (\x08) the way a terminal would, so
// a confirm repaint that readline draws as "<answer>\b<label>…" collapses to the
// clean "<label>…" it leaves on screen. Windows readline emits these erase
// sequences where the unix build does not, and they leak into the piped capture
// as a spurious "<answer>…" fragment line; resolving them here keeps PromptConfirm
// deterministic across hosts instead of locking one terminal's repaint bytes.
func applyBackspaces(s string) string {
	if !strings.ContainsRune(s, '\x08') {
		return s
	}
	for {
		next := backspacePair.ReplaceAllString(s, "")
		if next == s {
			// Any backspace with nothing left to erase is itself dropped.
			return strings.ReplaceAll(next, "\x08", "")
		}
		s = next
	}
}
