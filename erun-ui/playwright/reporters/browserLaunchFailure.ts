import * as path from 'node:path';

import type {
  FullResult,
  Reporter,
  TestCase,
  TestError,
  TestResult,
} from '@playwright/test/reporter';

// A browser that dies before it is ready is reported by Playwright as
// `browserType.launch: Target page, context or browser has been closed` — a
// generic with no cause in it — and the death is charged to whichever spec
// happened to ask for the worker's browser first. Both halves of that are
// misleading: the cause is a harness event (the headless_shell process dying of
// SIGSEGV ~1 ms into startup on this host), and the spec that absorbed it is an
// arbitrary one of the N specs the worker would have run.
//
// The cause is not missing, it is buried. Playwright's own error carries the
// whole browser log — the `<launched> pid=…` line, the browser's stderr
// (`ERROR:dbus/bus.cc:405 …`), and, in the call log, the
// `[pid=…] <process did exit: exitCode=null, signal=SIGSEGV>` line that names
// the death. Everything below the first line is exactly the diagnosis, and an
// error whose first line is a generic is an error whose diagnosis gets quoted
// away: reading `Target page, context or browser has been closed` out of a
// 3 MB gate log tells an operator nothing, and three separate areas each lost a
// run to it before anyone read far enough to see it was one death, not three
// defects.
//
// This reporter stops that: it recognises an error that carries a browser
// process death, and prints one delimited block that leads with the death and
// then reproduces Playwright's own browser log verbatim. Nothing is
// re-diagnosed and nothing is invented — the text was always there, and this
// only stops it being discarded.
//
// It never alters the run: it prints, and the failure stands exactly as it was.

// A browser log is bounded by Playwright's own collector, but the `<launching>`
// line is a several-hundred-argument command line that appears twice (once in
// the browser log, once in the call log). The cap keeps one block from
// dominating a gate log; the head and the tail are both kept, since the
// browser's own stderr is at the top and the death line at the bottom.
const MAX_PAYLOAD_CHARS = 8_192;

// browserProcessDeathLine returns Playwright's own record of the browser
// process's exit, or '' when the text carries none (which is what makes it a
// usable test for "this error is a browser death" rather than any other
// failure).
const BROWSER_EXIT_PATTERN = /<process did exit: exitCode=[^,]*, signal=[^>]*>/;

export function browserProcessDeathLine(message: string): string {
  const match = BROWSER_EXIT_PATTERN.exec(message);
  return match ? match[0] : '';
}

// browserStderrLines returns the lines the browser process itself wrote to
// stderr, which is where a dying Chromium says what it was doing first.
const BROWSER_STDERR_PATTERN = /^\[pid=\d+\]\[err\] (.*)$/;

function browserStderrLines(message: string): string[] {
  const lines: string[] = [];
  for (const line of message.split('\n')) {
    const match = BROWSER_STDERR_PATTERN.exec(line.trim());
    if (match?.[1] !== undefined && match[1].trim() !== '') {
      lines.push(match[1].trim());
    }
  }
  return lines;
}

// browserDeathSignature identifies one death, so a worker that is replaced and
// dies again is reported as the same defect rather than a fresh one. The pid
// and the timestamps differ every time, so neither is part of it.
function browserDeathSignature(message: string): string {
  return [browserProcessDeathLine(message), ...browserStderrLines(message)].join(' | ');
}

// boundPayload keeps a pathological browser log from dominating the gate log
// without ever hiding the death: the death line is already in the block's
// headline, and both ends of the payload are printed.
function boundPayload(message: string): string {
  if (message.length <= MAX_PAYLOAD_CHARS) {
    return message;
  }
  const half = Math.floor(MAX_PAYLOAD_CHARS / 2);
  const elided = message.length - MAX_PAYLOAD_CHARS;
  return (
    `${message.slice(0, half)}\n` +
    `… ${String(elided)} characters elided …\n` +
    message.slice(message.length - half)
  );
}

// formatBrowserLaunchReport is the block itself. It leads with the cause, names
// the spec the death was charged to so the misattribution is visible rather
// than inherited, and then reproduces Playwright's own text.
export function formatBrowserLaunchReport(message: string, chargedTo: string): string {
  const death = browserProcessDeathLine(message);
  const stderr = browserStderrLines(message);
  const lines = [
    '>> playwright: BROWSER LAUNCH FAILURE — the browser process died before it was ready.',
    '>> playwright: this is a harness event, not a defect in the spec it was charged to.',
    `>> playwright: charged to ${chargedTo}`,
    `>> playwright: ${death}`,
  ];
  if (stderr.length > 0) {
    lines.push('>> playwright: the browser wrote to stderr:');
    for (const line of stderr) {
      lines.push(`>> playwright:   ${line}`);
    }
  }
  lines.push(
    ">> playwright: Playwright's own browser log follows, verbatim:",
    boundPayload(message),
    ">> playwright: end of Playwright's browser log.",
  );
  return lines.join('\n');
}

const SUITE_DIR = path.join(__dirname, '..');

// suiteRelativeLocation names the spec the way the operator sees it in the
// run's own output, rather than as an absolute path under whoever's checkout
// this happens to be. A spec outside the suite — the harness's own regression
// spec runs the reporter over a throwaway project — keeps its absolute path
// rather than a chain of `..` segments, which names nothing.
function suiteRelativeLocation(test: TestCase): string {
  const relative = path.relative(SUITE_DIR, test.location.file);
  const file = relative.startsWith('..') ? test.location.file : relative;
  const line = test.location.line > 0 ? `:${String(test.location.line)}` : '';
  return `${file}${line} "${test.title}"`;
}

// BrowserLaunchFailureReporter prints one block per distinct browser death.
//
// Repeats are collapsed to a single line naming the count: a worker whose
// browser dies is replaced and the next worker may die identically, so the same
// death arrives once per restart. Printing the whole block each time is how one
// harness defect came to read as several unrelated spec failures.
class BrowserLaunchFailureReporter implements Reporter {
  private readonly deaths = new Map<string, number>();

  onTestEnd(test: TestCase, result: TestResult): void {
    for (const error of result.errors) {
      this.report(error, suiteRelativeLocation(test));
    }
  }

  onError(error: TestError): void {
    this.report(error, 'the run itself');
  }

  onEnd(_result: FullResult): void {
    const deaths = this.deaths.size;
    if (deaths === 0) {
      return;
    }
    const occurrences = [...this.deaths.values()].reduce((sum, n) => sum + n, 0);
    process.stderr.write(
      `>> playwright: ${String(occurrences)} browser launch failure(s) in this run, ` +
        `${String(deaths)} distinct signature(s) — the browser died at startup, not a spec.\n`,
    );
  }

  private report(error: TestError, chargedTo: string): void {
    const message = error.message ?? '';
    const death = browserProcessDeathLine(message);
    if (death === '') {
      return;
    }
    const signature = browserDeathSignature(message);
    const seen = this.deaths.get(signature) ?? 0;
    this.deaths.set(signature, seen + 1);
    if (seen > 0) {
      process.stderr.write(
        `>> playwright: BROWSER LAUNCH FAILURE again — same signature as the block above ` +
          `(occurrence ${String(seen + 1)}), charged to ${chargedTo}: ${death}\n`,
      );
      return;
    }
    process.stderr.write(`${formatBrowserLaunchReport(message, chargedTo)}\n`);
  }
}

export default BrowserLaunchFailureReporter;
