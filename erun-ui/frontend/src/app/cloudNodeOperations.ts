import type { CloudNodeOperation } from './model';

// cloudNodeOperationFor answers "is there an operation in flight against THIS
// node", which is the only form of the question a label naming one node may
// ask. The flag it replaced was a single global boolean, so a start on one
// environment's node put a progressive label on every other environment's
// control — including environments with nothing running at all.
export function cloudNodeOperationFor(
  operations: Record<string, CloudNodeOperation>,
  name: string | undefined,
): CloudNodeOperation | null {
  const key = (name ?? '').trim();
  if (key === '') {
    return null;
  }
  return operations[key] ?? null;
}
