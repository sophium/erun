import type { SelectFieldOption } from 'erun-kit';

import type { OrchestratorAlias } from '@/app/slices/orchestratorsSlice';

// OrchestratorAliasNone is the picker's sentinel for "this orchestrator
// declares no alias of its own". Radix's Select.Item rejects an empty-string
// value, so '' travels as this token and is translated back at the boundary by
// orchestratorAliasValue -- the same round trip the role picker makes for
// undeclared. A configured alias can never collide with it: an erun alias is
// `username+account@erun` (eruncommon.CloudProviderAlias).
export const ORCHESTRATOR_ALIAS_NONE = 'none';

// orchestratorAliasFieldId is the control's id. The dialog spec locates the
// trigger by it, so it is derived here rather than spelled at the call site.
export const ORCHESTRATOR_ALIAS_FIELD_ID = 'orchestrator-alias';

// orchestratorAliasOptions lists the values the alias picker can carry: the
// host's configured erun aliases, plus "none" for declaring none of its own.
//
// It deliberately also carries an alias the host no longer resolves. A value
// can be on disk from config.yaml edited by hand, or from an alias removed from
// this machine since it was recorded, and Edit mode has to show the operator
// what is actually stored -- otherwise the trigger renders blank, no item
// matches its value, and the operator cannot see the value the next save is
// about to refuse. This mirrors orchestratorEnvRoleOptions' treatment of a role
// the gate would now reject.
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

// orchestratorAliasValue translates the picker's own value back into the field
// the backend stores: the sentinel becomes '', every alias is itself.
export function orchestratorAliasValue(raw: string): OrchestratorAlias {
  return raw === ORCHESTRATOR_ALIAS_NONE ? '' : raw;
}

// orchestratorAliasHelper states, briefly, what the selection means.
export function orchestratorAliasHelper(alias: OrchestratorAlias): string {
  if (alias === '') {
    return "No alias of its own — this orchestrator follows this machine's own erun alias.";
  }
  return `Declares ${alias} as this orchestrator's own platform alias.`;
}
