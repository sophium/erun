import { Plus, Trash2 } from 'lucide-react';
import * as React from 'react';

import { EditableComboField } from '@/components/app/EditableComboField';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import type { UIContainerRegistryEntry } from '@/types';

// REGISTRY_ROLES is the marked-list vocabulary, in build → copy → deploy order.
// Kept in lockstep with eruncommon.RegistryRole.
const REGISTRY_ROLES = ['build', 'from', 'to', 'deploy'] as const;
type RegistryRole = (typeof REGISTRY_ROLES)[number];

const ROLE_HINT: Record<RegistryRole, string> = {
  build: 'erun build/push target',
  from: 'copy source on deploy',
  to: 'copy destination on deploy',
  deploy: 'cluster pulls from here',
};

// ContainerRegistriesField edits an environment's marked registry list: rows of
// a registry host plus build/from/to/deploy role toggles, with add/remove. An
// empty list inherits the project default. A live validation hint mirrors the
// backend marker invariants so the operator sees the problem before saving
// (the backend Validate() is the authoritative gate). Issue #527.
export function ContainerRegistriesField({
  entries,
  suggestions,
  disabled,
  onChange,
}: {
  entries: UIContainerRegistryEntry[];
  suggestions: string[];
  disabled?: boolean;
  onChange: (next: UIContainerRegistryEntry[]) => void;
}): React.ReactElement {
  const updateRow = (index: number, next: UIContainerRegistryEntry): void => {
    onChange(entries.map((entry, idx) => (idx === index ? next : entry)));
  };
  const hint = registriesValidationHint(entries);
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between">
        <Label>Container registries</Label>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled}
          onClick={() => {
            onChange([...entries, { registry: '', roles: ['build', 'deploy'] }]);
          }}
          aria-label="Add registry"
        >
          <Plus aria-hidden="true" />
          Add registry
        </Button>
      </div>
      {entries.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          Inherits the project default registry. Add a registry to override which registries this
          environment builds to, copies between, and the cluster pulls from.
        </p>
      ) : (
        <div className="overflow-hidden rounded-[var(--radius)] border border-border">
          {entries.map((entry, index) => (
            <RegistryRow
              key={index}
              entry={entry}
              index={index}
              suggestions={suggestions}
              disabled={disabled}
              onChange={(next) => {
                updateRow(index, next);
              }}
              onRemove={() => {
                onChange(entries.filter((_, idx) => idx !== index));
              }}
            />
          ))}
        </div>
      )}
      {hint ? (
        <p role="alert" className="text-xs text-amber-600 dark:text-amber-500">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

function RegistryRow({
  entry,
  index,
  suggestions,
  disabled,
  onChange,
  onRemove,
}: {
  entry: UIContainerRegistryEntry;
  index: number;
  suggestions: string[];
  disabled?: boolean;
  onChange: (next: UIContainerRegistryEntry) => void;
  onRemove: () => void;
}): React.ReactElement {
  const toggleRole = (role: RegistryRole, checked: boolean): void => {
    const roles = checked
      ? Array.from(new Set([...entry.roles, role]))
      : entry.roles.filter((value) => value !== role);
    onChange({ ...entry, roles });
  };
  const label = `Registry ${String(index + 1)}`;
  return (
    <div
      data-border={index > 0}
      className="grid gap-2 border-border px-3 py-2.5 data-[border=true]:border-t"
    >
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2">
        <EditableComboField
          id={`environment-config-registry-${String(index)}`}
          label={label}
          value={entry.registry}
          suggestions={suggestions}
          disabled={disabled}
          onValueChange={(registry) => {
            onChange({ ...entry, registry });
          }}
        />
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="mb-0.5 text-muted-foreground"
          disabled={disabled}
          aria-label={`Remove registry ${String(index + 1)}`}
          onClick={onRemove}
        >
          <Trash2 aria-hidden="true" />
        </Button>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1.5">
        {REGISTRY_ROLES.map((role) => {
          const checkboxId = `environment-config-registry-${String(index)}-role-${role}`;
          return (
            <label
              key={role}
              htmlFor={checkboxId}
              className="flex items-center gap-1.5 text-xs text-muted-foreground"
              title={ROLE_HINT[role]}
            >
              <Checkbox
                id={checkboxId}
                checked={entry.roles.includes(role)}
                disabled={disabled}
                aria-label={`${role} role for registry ${String(index + 1)}`}
                onCheckedChange={(checked) => {
                  toggleRole(role, checked === true);
                }}
              />
              {role}
            </label>
          );
        })}
      </div>
    </div>
  );
}

// registriesValidationHint mirrors eruncommon.ContainerRegistries.Validate so
// the operator gets immediate, inline guidance (Nielsen #5 error prevention).
// The backend remains the authoritative gate; this only previews the same rule.
function registriesValidationHint(entries: UIContainerRegistryEntry[]): string {
  const rows = entries.filter((entry) => entry.registry.trim() !== '');
  if (rows.length === 0) {
    return '';
  }
  const withRole = (role: RegistryRole): UIContainerRegistryEntry[] =>
    rows.filter((entry) => entry.roles.includes(role));
  if (withRole('build').length > 1) {
    return 'Only one registry can be marked build.';
  }
  if (withRole('from').length > 1) {
    return 'Only one registry can be marked from.';
  }
  if (withRole('deploy').length === 0) {
    return 'At least one registry must be marked deploy.';
  }
  const fromRows = withRole('from');
  const toRows = withRole('to');
  if (fromRows.length > 0 !== toRows.length > 0) {
    return 'From and to must be set together (a copy needs both a source and a destination).';
  }
  const fromRegistry = fromRows[0]?.registry.trim();
  if (fromRegistry && toRows.some((entry) => entry.registry.trim() === fromRegistry)) {
    return 'A registry cannot be both from and to.';
  }
  return '';
}
