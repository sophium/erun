import { Button, Checkbox, IconTooltip } from 'erun-kit';
import { Bot, ListChecks, Server } from 'lucide-react';
import * as React from 'react';

import type { WhipTargetSelection } from '@/app/model';

import type { main } from '../../../wailsjs/go/models';

interface TargetRow {
  id: string;
  label: string;
  checked: boolean;
}

// The three group shortcuts, each carrying its one human-readable name. The
// buttons are icon-only, so IconTooltip's label is the only text an operator
// can see -- and a tooltip is associated with its trigger as a *description*,
// not as a name, so the accessible name has to be set separately. Writing that
// same string out twice by hand is how the third shortcut came to announce
// "Select all" while its tooltip read "Select all orchestrators and
// environments": WCAG 2.5.3 "Label in Name" requires the accessible name to
// contain the visible label, and voice control ("click Select all
// orchestrators and environments") had nothing in the name to match. One
// constant per shortcut, consumed by both, makes that divergence
// unrepresentable rather than fixing one instance of it.
const GROUP_SHORTCUTS = [
  { key: 'orchestrators', label: 'Select all orchestrators', icon: Bot },
  { key: 'environments', label: 'Select all environments', icon: Server },
  { key: 'all', label: 'Select all orchestrators and environments', icon: ListChecks },
] as const;

// isWhipSelectionEmpty is true only for the untouched default: nothing
// focused, nothing manually checked either. Pulled out of the component body
// to keep its own branching out of the render function's complexity budget.
function isWhipSelectionEmpty(selection: WhipTargetSelection): boolean {
  return (
    selection.environmentMode === 'custom' &&
    selection.orchestratorMode === 'custom' &&
    selection.selectedEnvironmentIds.length === 0 &&
    selection.selectedOrchestratorIds.length === 0
  );
}

function orchestratorRows(
  targets: main.uiWhipTargetList,
  selection: WhipTargetSelection,
): TargetRow[] {
  return targets.orchestrators.map((orchestrator) => ({
    id: orchestrator.id,
    label: orchestrator.name,
    checked:
      selection.orchestratorMode === 'all' ||
      selection.selectedOrchestratorIds.includes(orchestrator.id),
  }));
}

function environmentRows(
  targets: main.uiWhipTargetList,
  selection: WhipTargetSelection,
): TargetRow[] {
  return targets.environments.map((environment) => ({
    id: environment.id,
    label: environment.id,
    checked:
      selection.environmentMode === 'all' ||
      selection.selectedEnvironmentIds.includes(environment.id),
  }));
}

// TitlebarWhipTargetPicker is the selection surface issue erun#1700 asks for:
// individual environments and orchestrators, checkable, plus the three group
// shortcuts. Plain checkboxes and buttons rather than a Command/cmdk list:
// the population here is short and never needs type-to-filter, and every row
// is reachable with a plain Tab (WCAG 2.1.1 keyboard) the same way the
// existing deploy-components and orchestrator-environments checklists
// already are. The primary whip action lives in the popover header
// (Titlebar.WhipAction.tsx) rather than here, so it stays reachable without
// scrolling this list (erun#1748).
export function TitlebarWhipTargetPicker({
  targets,
  targetsLoading,
  selection,
  onToggleEnvironment,
  onToggleOrchestrator,
  onSelectAllEnvironments,
  onSelectAllOrchestrators,
  onSelectAll,
}: {
  targets: main.uiWhipTargetList | null;
  targetsLoading: boolean;
  selection: WhipTargetSelection;
  onToggleEnvironment: (id: string, checked: boolean) => void;
  onToggleOrchestrator: (id: string, checked: boolean) => void;
  onSelectAllEnvironments: () => void;
  onSelectAllOrchestrators: () => void;
  onSelectAll: () => void;
}): React.ReactElement {
  const groupShortcutHandlers: Record<(typeof GROUP_SHORTCUTS)[number]['key'], () => void> = {
    orchestrators: onSelectAllOrchestrators,
    environments: onSelectAllEnvironments,
    all: onSelectAll,
  };
  return (
    <div className="flex flex-col gap-3">
      {isWhipSelectionEmpty(selection) && (
        <p className="text-xs text-muted-foreground">
          Nothing is focused right now. Choose one or more targets below.
        </p>
      )}
      <div className="flex items-center gap-1.5">
        {GROUP_SHORTCUTS.map(({ key, label, icon: Icon }) => (
          <IconTooltip key={key} label={label}>
            <Button
              type="button"
              variant="outline"
              size="icon-sm"
              aria-label={label}
              onClick={groupShortcutHandlers[key]}
            >
              <Icon aria-hidden="true" className="size-4" />
            </Button>
          </IconTooltip>
        ))}
      </div>
      {targetsLoading || !targets ? (
        <p className="text-xs text-muted-foreground">Loading targets…</p>
      ) : (
        <div className="flex max-h-[40vh] flex-col gap-3 overflow-y-auto">
          <TargetSection
            heading="Orchestrators"
            emptyText="No orchestrators configured."
            rows={orchestratorRows(targets, selection)}
            onToggle={onToggleOrchestrator}
          />
          <TargetSection
            heading="Environments"
            emptyText="No environments available to whip."
            rows={environmentRows(targets, selection)}
            onToggle={onToggleEnvironment}
          />
        </div>
      )}
    </div>
  );
}

function TargetSection({
  heading,
  emptyText,
  rows,
  onToggle,
}: {
  heading: string;
  emptyText: string;
  rows: TargetRow[];
  onToggle: (id: string, checked: boolean) => void;
}): React.ReactElement {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs font-semibold text-muted-foreground uppercase">{heading}</span>
      {rows.length === 0 ? (
        <p className="text-xs text-muted-foreground">{emptyText}</p>
      ) : (
        rows.map((row) => (
          <label key={row.id} className="flex items-center gap-2 py-0.5 text-xs">
            <Checkbox
              checked={row.checked}
              onCheckedChange={(checked) => {
                onToggle(row.id, checked === true);
              }}
            />
            <span className="min-w-0 flex-1 truncate">{row.label}</span>
          </label>
        ))
      )}
    </div>
  );
}
