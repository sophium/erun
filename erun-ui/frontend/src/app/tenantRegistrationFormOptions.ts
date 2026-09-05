// Plain constants/helpers for the Registration tab's forms, kept in a
// non-component module so TenantDashboardPanels.RegistrationForms.tsx can
// stay component-only (react-refresh/only-export-components).
import type { UITenantDashboard } from '@/types';

export const ENV_TYPE_OPTIONS = [
  { value: 'runtime', label: 'runtime' },
  { value: 'remote-agent', label: 'remote-agent' },
  { value: 'local-agent', label: 'local-agent' },
];

// NO_CONTEXT is a Radix Select sentinel: it cannot carry an empty-string
// item value, so "deploy onto a bare kubernetes context instead" needs a
// value distinct from every real contextId.
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
