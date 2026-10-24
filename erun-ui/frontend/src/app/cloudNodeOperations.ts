import type { CloudNodeOperation } from './model';

// cloudNodeOperationFor scopes in-flight operations to one node, so a start on
// one node does not mark every other control as progressing.
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
