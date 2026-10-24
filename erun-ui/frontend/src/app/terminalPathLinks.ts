// Absolute paths only: relative paths need a per-session cwd, which is not tracked.
export interface PathMatch {
  readonly start: number;
  readonly end: number;
  readonly text: string;
}

const URL_SPAN = /https?:\/\/[^\s<>"'`]+/g;

const ABS_POSIX_PATH = /(?<![A-Za-z0-9_])\/(?:[^\s"'`<>|:]*[^\s"'`<>|.,;:!?)\]}])/g;
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

// findAbsolutePathMatches skips spans already covered by a URL.
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
