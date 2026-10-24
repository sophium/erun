// Not Redux state: recording actions into the store would feed back into itself.

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

export function formatUITrace(list: UITraceEntry[]): string {
  return list
    .map((entry) => {
      const at = new Date(entry.at).toISOString();
      const changed = entry.changed.length > 0 ? `  →  ${entry.changed.join(', ')}` : '';
      return `${at}  ${entry.type}${changed}`;
    })
    .join('\n');
}
