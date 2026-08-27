// Pure text matching for absolute file paths inside one rendered terminal
// line. Kept free of xterm/DOM so the matching rules are unit-testable on
// their own; TerminalPathLinkProvider maps the resulting string offsets onto
// buffer columns and decides host vs pod resolution.
//
// Deliberately absolute-path only: a relative path needs the session's
// current working directory to resolve, and this codebase has no per-session
// cwd tracking today (a PTY's cwd changes with every `cd`, so tracking it
// accurately needs shell-side cooperation, e.g. OSC 7 -- a larger, separate
// feature). Out of scope here, matching the precedent #1354 itself set for
// line/column targets.
export interface PathMatch {
  readonly start: number;
  readonly end: number;
  readonly text: string;
}

const URL_SPAN = /https?:\/\/[^\s<>"'`]+/g;

// Absolute POSIX path: not preceded by an alphanumeric (excludes "12/25",
// "and/or", "km/h" -- a bare "/" is not part of a larger token), and not
// ending on trailing sentence punctuation or a closing bracket a shell would
// never include in the path itself.
const ABS_POSIX_PATH = /(?<![A-Za-z0-9_])\/(?:[^\s"'`<>|:]*[^\s"'`<>|.,;:!?)\]}])/g;
// Absolute Windows path: DRIVE:\...
const ABS_WINDOWS_PATH = /(?<![A-Za-z0-9_])[A-Za-z]:\\(?:[^\s"'`<>|:]*[^\s"'`<>|.,;:!?)\]}])/g;

function excludedSpans(line: string): [number, number][] {
  const spans: [number, number][] = [];
  for (const match of line.matchAll(URL_SPAN)) {
    const start = match.index;
    spans.push([start, start + match[0].length]);
  }
  return spans;
}

function overlaps(spans: [number, number][], start: number, end: number): boolean {
  return spans.some(([spanStart, spanEnd]) => start < spanEnd && end > spanStart);
}

// findAbsolutePathMatches returns non-overlapping absolute-path candidates in
// a single rendered line, excluding any span already covered by a URL (so a
// path segment inside "https://host/foo" is never double-registered as its
// own link).
export function findAbsolutePathMatches(line: string): PathMatch[] {
  const exclude = excludedSpans(line);
  const matches: PathMatch[] = [];
  for (const pattern of [ABS_POSIX_PATH, ABS_WINDOWS_PATH]) {
    for (const match of line.matchAll(pattern)) {
      const start = match.index;
      const text = match[0];
      const end = start + text.length;
      if (overlaps(exclude, start, end)) {
        continue;
      }
      matches.push({ start, end, text });
    }
  }
  matches.sort((a, b) => a.start - b.start);
  return matches;
}
