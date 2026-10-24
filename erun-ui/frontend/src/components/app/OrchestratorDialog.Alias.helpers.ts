import type { SelectFieldOption } from 'erun-kit';

import type { OrchestratorAlias } from '@/app/slices/orchestratorsSlice';

// ORCHESTRATOR_ALIAS_NONE stands in for '' because Radix Select rejects empty values; erun aliases contain '@', so cannot collide.
export const ORCHESTRATOR_ALIAS_NONE = 'none';

// ORCHESTRATOR_ALIAS_FIELD_ID is the control's id; the dialog spec locates the trigger by it.
export const ORCHESTRATOR_ALIAS_FIELD_ID = 'orchestrator-alias';

// orchestratorAliasOptions lists configured aliases plus none, keeping a stored alias the host no longer resolves.
export function orchestratorAliasOptions(
  configured: string[],
  alias: OrchestratorAlias,
): SelectFieldOption[] {
  const options: SelectFieldOption[] = [
    { value: ORCHESTRATOR_ALIAS_NONE, label: "This machine's own alias" },
    ...configured.map((value) => ({ value, label: value })),
  ];
  if (alias !== '' && !options.some((option) => option.value === alias)) {
    options.push({ value: alias, label: alias });
  }
  return options;
}

// orchestratorAliasValue maps the sentinel back to ''.
export function orchestratorAliasValue(raw: string): OrchestratorAlias {
  return raw === ORCHESTRATOR_ALIAS_NONE ? '' : raw;
}

export function orchestratorAliasHelper(alias: OrchestratorAlias): string {
  if (alias === '') {
    return "No alias of its own — this orchestrator follows this machine's own erun alias.";
  }
  return `Declares ${alias} as this orchestrator's own platform alias.`;
}
