// The Diagnostics console's in-app stand-in for Redux DevTools, which the
// packaged WebView cannot provide.
// Deliberately NOT Redux state: recording into the store would make every
// recorded action dispatch another write — a feedback loop — so the console
// polls this module instead.

export interface UITraceEntry {
  at: number;
  type: string;
  changed: string[];
}

const MAX_ENTRIES = 500;

let entries: UITraceEntry[] = [];
let generation = 0;

export function recordUITraceEntry(entry: UITraceEntry): void {
  entries.push(entry);
  if (entries.length > MAX_ENTRIES) {
    entries = entries.slice(entries.length - MAX_ENTRIES);
  }
  generation++;
}

// uiTraceGeneration lets the console cheaply detect "anything new?" without
// copying the buffer on every poll tick.
export function uiTraceGeneration(): number {
  return generation;
}

export function uiTraceEntries(): UITraceEntry[] {
  return entries.slice();
}

export function clearUITrace(): void {
  entries = [];
  generation++;
}

// formatUITrace renders the history as paste-ready text for an error report.
export function formatUITrace(list: UITraceEntry[]): string {
  return list
    .map((entry) => {
      const at = new Date(entry.at).toISOString();
      const changed = entry.changed.length > 0 ? `  →  ${entry.changed.join(', ')}` : '';
      return `${at}  ${entry.type}${changed}`;
    })
    .join('\n');
}
