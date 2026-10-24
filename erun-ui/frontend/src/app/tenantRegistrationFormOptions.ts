// Kept out of the component module for react-refresh/only-export-components.
import type { UITenantDashboard } from '@/types';

export const ENV_TYPE_OPTIONS = [
  { value: 'runtime', label: 'runtime' },
  { value: 'remote-agent', label: 'remote-agent' },
  { value: 'local-agent', label: 'local-agent' },
];

// NO_CONTEXT stands in for "no context": Radix Select items cannot use ''.
export const NO_CONTEXT = '__none__';

export function contextOptions(
  data: UITenantDashboard | null | undefined,
): { value: string; label: string }[] {
  const contexts = data?.contexts ?? [];
  return [
    { value: NO_CONTEXT, label: '— none (use a kubernetes context below) —' },
    ...contexts.map((context) => ({
      value: context.contextId,
      label: `${context.name} (${context.status})`,
    })),
  ];
}
