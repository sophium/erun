import * as path from 'node:path';

import type {
  FullResult,
  Reporter,
  TestCase,
  TestError,
  TestResult,
} from '@playwright/test/reporter';

// Playwright reports a browser that dies at launch as a cause-free generic charged to an
// arbitrary spec; this reporter prints one block leading with the death, followed by
// Playwright's own browser log verbatim. It never alters the run's outcome.

// Head and tail are both kept: the browser's stderr is at the top, the death line at the bottom.
const MAX_PAYLOAD_CHARS = 8_192;

// Empty when the text carries no browser exit, which makes it the browser-death test.
const BROWSER_EXIT_PATTERN = /<process did exit: exitCode=[^,]*, signal=[^>]*>/;

export function browserProcessDeathLine(message: string): string {
  const match = BROWSER_EXIT_PATTERN.exec(message);
  return match ? match[0] : '';
}

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

// Excludes pid and timestamps so a replaced worker dying again counts as the same death.
function browserDeathSignature(message: string): string {
  return [browserProcessDeathLine(message), ...browserStderrLines(message)].join(' | ');
}

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

// A spec outside the suite keeps its absolute path rather than a chain of `..`.
function suiteRelativeLocation(test: TestCase): string {
  const relative = path.relative(SUITE_DIR, test.location.file);
  const file = relative.startsWith('..') ? test.location.file : relative;
  const line = test.location.line > 0 ? `:${String(test.location.line)}` : '';
  return `${file}${line} "${test.title}"`;
}

// Repeats of the same death (one per worker restart) collapse to a count line.
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
