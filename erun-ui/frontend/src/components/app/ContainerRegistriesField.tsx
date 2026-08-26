import { Button, Checkbox, EditableComboField, IconTooltip, Label } from 'erun-kit';
import { Boxes, Plus, Trash2 } from 'lucide-react';
import * as React from 'react';

import type { UIContainerRegistryEntry } from '@/types';

// Mirrors eruncommon.RegistryRole and must stay in lockstep; ordered by build → copy → deploy pipeline phase.
const REGISTRY_ROLES = ['build', 'from', 'to', 'deploy'] as const;
type RegistryRole = (typeof REGISTRY_ROLES)[number];

const ROLE_HINT: Record<RegistryRole, string> = {
  build: 'erun build/push target',
  from: 'copy source on deploy',
  to: 'copy destination on deploy',
  deploy: 'cluster pulls from here',
};

// ContainerRegistriesField edits an environment's registry list, or inherits the project default when the list is empty. Its inline hint previews the backend marker rules so operators can fix problems before saving. Inherited-from-project and in-cluster (`cluster:`) registries render as legible read-only rows rather than blank inputs.
export function ContainerRegistriesField({
  entries,
  inherited,
  suggestions,
  disabled,
  onChange,
}: {
  entries: UIContainerRegistryEntry[];
  inherited?: boolean;
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
          id="environment-config-add-registry"
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
      {inherited && entries.length > 0 ? (
        <p className="text-xs text-muted-foreground">
          Inherited from this project&apos;s configuration. Add a registry to override it for this
          environment.
        </p>
      ) : null}
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
        <p role="alert" className="text-xs text-amber-700 dark:text-amber-400">
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
  // A cluster entry names no static host, so it renders as a legible read-only
  // row (never a blank text box). Its addresses resolve from the env's
  // Kubernetes context, so there is nothing to type here.
  if (entry.cluster) {
    return (
      <ClusterRegistryRow entry={entry} index={index} disabled={disabled} onRemove={onRemove} />
    );
  }
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
      <RegistryRoleCheckboxes
        index={index}
        entry={entry}
        disabled={disabled}
        onToggle={toggleRole}
      />
    </div>
  );
}

function RegistryRoleCheckboxes({
  index,
  entry,
  disabled,
  onToggle,
}: {
  index: number;
  entry: UIContainerRegistryEntry;
  disabled?: boolean;
  onToggle: (role: RegistryRole, checked: boolean) => void;
}): React.ReactElement {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1.5">
      {REGISTRY_ROLES.map((role) => {
        const checkboxId = `environment-config-registry-${String(index)}-role-${role}`;
        return (
          <IconTooltip key={role} label={ROLE_HINT[role]}>
            <label
              htmlFor={checkboxId}
              className="flex items-center gap-1.5 text-xs text-muted-foreground"
            >
              <Checkbox
                id={checkboxId}
                checked={entry.roles.includes(role)}
                disabled={disabled}
                aria-label={`${role} role for registry ${String(index + 1)}`}
                onCheckedChange={(checked) => {
                  onToggle(role, checked === true);
                }}
              />
              {role}
            </label>
          </IconTooltip>
        );
      })}
    </div>
  );
}

// ClusterRegistryRow shows a context-resolved in-cluster registry legibly. The
// host is not editable (it resolves from the Kubernetes context), so the roles
// render as read-only badges rather than checkboxes.
function ClusterRegistryRow({
  entry,
  index,
  disabled,
  onRemove,
}: {
  entry: UIContainerRegistryEntry;
  index: number;
  disabled?: boolean;
  onRemove: () => void;
}): React.ReactElement {
  const cluster = entry.cluster;
  const label = cluster?.label ?? 'in-cluster registry';
  const activeRoles = REGISTRY_ROLES.filter((role) => entry.roles.includes(role));
  return (
    <div
      data-border={index > 0}
      className="grid gap-1.5 border-border px-3 py-2.5 data-[border=true]:border-t"
    >
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-2">
        <div className="grid min-w-0 gap-0.5">
          <div className="flex min-w-0 items-center gap-2 text-sm">
            <Boxes className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="truncate font-medium">In-cluster registry</span>
          </div>
          <div className="truncate pl-6 text-xs text-muted-foreground [overflow-wrap:anywhere]">
            {label} — resolved from this environment&apos;s Kubernetes context
          </div>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="text-muted-foreground"
          disabled={disabled}
          aria-label={`Remove registry ${String(index + 1)}`}
          onClick={onRemove}
        >
          <Trash2 aria-hidden="true" />
        </Button>
      </div>
      <div className="flex flex-wrap gap-1.5 pl-6">
        {activeRoles.map((role) => (
          <span
            key={role}
            className="rounded-full border border-border px-2 py-0.5 text-[11px] text-muted-foreground"
          >
            {role}
          </span>
        ))}
      </div>
    </div>
  );
}

// registriesValidationHint previews eruncommon.ContainerRegistries.Validate inline so operators catch marker problems before saving; the backend stays the authoritative gate.
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
