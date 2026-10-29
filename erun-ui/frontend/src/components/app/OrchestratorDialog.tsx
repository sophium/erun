import { Button } from 'erun-kit';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from 'erun-kit';
import { Input } from 'erun-kit';
import { Label } from 'erun-kit';
import { SelectField } from 'erun-kit';
import { AlertTriangle, LoaderCircle, RotateCcw, Trash2 } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  createOrchestrator,
  deleteOrchestrator,
  restartOrchestrator,
  updateOrchestrator,
} from '@/app/orchestratorThunks';
import {
  closeOrchestratorDialog,
  type OrchestratorAlias,
  type OrchestratorEnvRef,
  type OrchestratorEnvRole,
  type OrchestratorInfo,
} from '@/app/slices/orchestratorsSlice';
import { InlineAlert } from '@/components/app/InlineAlert';
import {
  ORCHESTRATOR_ALIAS_FIELD_ID,
  ORCHESTRATOR_ALIAS_NONE,
  orchestratorAliasHelper,
  orchestratorAliasOptions,
  orchestratorAliasValue,
} from '@/components/app/OrchestratorDialog.Alias.helpers';
import { DirectoriesField } from '@/components/app/OrchestratorDialog.Directories';
import { EnvironmentsField } from '@/components/app/OrchestratorDialog.Environments';
import {
  type EnvCandidate,
  envKey,
} from '@/components/app/OrchestratorDialog.Environments.helpers';
import { OrchestratorGuidanceSection } from '@/components/app/OrchestratorDialog.Guidance';
import { OrchestratorLaunchFields } from '@/components/app/OrchestratorDialog.Launch';
import { orchestratorLaunchSettingsToSave } from '@/components/app/OrchestratorDialog.Launch.helpers';
import {
  type OrchestratorLaunchForm,
  useOrchestratorLaunchForm,
} from '@/components/app/OrchestratorDialog.Launch.hooks';
import { RestartRequiredNotice } from '@/components/app/OrchestratorDialog.RestartNotice';
import { restartNoticeText } from '@/components/app/OrchestratorDialog.RestartNotice.helpers';

import {
  ListOrchestratorAliasChoices,
  ListOrchestratorEnvCandidates,
} from '../../../wailsjs/go/main/App';

// An orchestrator: a name, linked environments with their directories, and directories of its own.
interface OrchestratorForm {
  candidates: EnvCandidate[];
  name: string;
  setName: (name: string) => void;
  // The picker offers only resolvable aliases: the writer refuses any other.
  aliasChoices: string[];
  alias: OrchestratorAlias;
  setAlias: (alias: OrchestratorAlias) => void;
  launch: OrchestratorLaunchForm;
  selected: OrchestratorEnvRef[];
  toggle: (candidate: EnvCandidate, checked: boolean) => void;
  setDirectory: (ref: OrchestratorEnvRef, directory: string) => void;
  setRole: (ref: OrchestratorEnvRef, role: OrchestratorEnvRole) => void;
  directories: string[];
  addDirectory: (directory: string) => void;
  removeDirectory: (directory: string) => void;
  submit: () => void;
}

function useOrchestratorForm(open: boolean, editing: OrchestratorInfo | null): OrchestratorForm {
  const dispatch = useAppDispatch();
  const [candidates, setCandidates] = React.useState<EnvCandidate[]>([]);
  const [name, setName] = React.useState('');
  const [alias, setAlias] = React.useState<OrchestratorAlias>('');
  const [aliasChoices, setAliasChoices] = React.useState<string[]>([]);
  const [selected, setSelected] = React.useState<OrchestratorEnvRef[]>([]);
  const [directories, setDirectories] = React.useState<string[]>([]);
  const launch = useOrchestratorLaunchForm(open, editing);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    setName(editing?.name ?? '');
    // Seed from the stored value so an alias the host no longer resolves still shows.
    setAlias(editing?.alias ?? '');
    setSelected(editing ? editing.environments.map((env) => ({ ...env })) : []);
    setDirectories(editing ? [...editing.directories] : []);
    void ListOrchestratorEnvCandidates().then((list) => {
      // The Wails binding types requiredRole as a plain string.
      setCandidates(list as EnvCandidate[]);
    });
    void ListOrchestratorAliasChoices().then((list) => {
      setAliasChoices(list);
    });
  }, [open, editing]);

  const toggle = (candidate: EnvCandidate, checked: boolean): void => {
    setSelected((current) => {
      const rest = current.filter(
        (ref) =>
          envKey(ref.tenant, ref.environment) !== envKey(candidate.tenant, candidate.environment),
      );
      return checked
        ? [
            ...rest,
            {
              tenant: candidate.tenant,
              environment: candidate.environment,
              directory: candidate.defaultDirectory,
              // Undeclared by default; a required role is the only legal value, so it is preselected.
              role: candidate.requiredRole ?? '',
            },
          ]
        : rest;
    });
  };

  const setDirectory = (ref: OrchestratorEnvRef, directory: string): void => {
    setSelected((current) =>
      current.map((entry) =>
        envKey(entry.tenant, entry.environment) === envKey(ref.tenant, ref.environment)
          ? { ...entry, directory }
          : entry,
      ),
    );
  };

  const setRole = (ref: OrchestratorEnvRef, role: OrchestratorEnvRole): void => {
    setSelected((current) =>
      current.map((entry) =>
        envKey(entry.tenant, entry.environment) === envKey(ref.tenant, ref.environment)
          ? { ...entry, role }
          : entry,
      ),
    );
  };

  // Deduped, like the backend.
  const addDirectory = (directory: string): void => {
    setDirectories((current) => (current.includes(directory) ? current : [...current, directory]));
  };
  const removeDirectory = (directory: string): void => {
    setDirectories((current) => current.filter((entry) => entry !== directory));
  };

  const submit = (): void => {
    const settings = orchestratorLaunchSettingsToSave(launch.settings);
    if (editing) {
      void dispatch(updateOrchestrator(editing.id, name, selected, directories, alias, settings));
    } else {
      void dispatch(createOrchestrator(name, selected, directories, alias, settings));
    }
  };

  return {
    candidates,
    name,
    setName,
    aliasChoices,
    alias,
    setAlias,
    launch,
    selected,
    toggle,
    setDirectory,
    setRole,
    directories,
    addDirectory,
    removeDirectory,
    submit,
  };
}

// OrchestratorDialog is the orchestrator's management surface, mirroring the env Manage dialog.
export function OrchestratorDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const open = useAppSelector((state) => state.orchestrators.dialogOpen);
  const editing = useAppSelector((state) => state.orchestrators.editing);
  const [confirmingDelete, setConfirmingDelete] = React.useState(false);

  // Reset on close so reopening never starts mid-confirmation.
  React.useEffect(() => {
    if (!open) {
      setConfirmingDelete(false);
    }
  }, [open]);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeOrchestratorDialog());
        }
      }}
    >
      <DialogContent
        // Bounded to the viewport with a scrolling field region so Save stays reachable.
        className="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-lg"
      >
        {editing && confirmingDelete ? (
          <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-6 py-6">
            <OrchestratorDeleteConfirm
              editing={editing}
              onCancel={() => {
                setConfirmingDelete(false);
              }}
            />
          </div>
        ) : (
          <OrchestratorForm
            open={open}
            editing={editing}
            onRequestDelete={() => {
              setConfirmingDelete(true);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

// A directory of its own counts as scope.
function orchestratorScopeIsEmpty(selected: OrchestratorEnvRef[], directories: string[]): boolean {
  return selected.length === 0 && directories.length === 0;
}

function OrchestratorForm({
  open,
  editing,
  onRequestDelete,
}: {
  open: boolean;
  editing: OrchestratorInfo | null;
  onRequestDelete: () => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((state) => state.orchestrators.busy);
  const error = useAppSelector((state) => state.orchestrators.error);
  const form = useOrchestratorForm(open, editing);
  const { candidates, name, selected, toggle, setDirectory, setRole, directories } = form;

  return (
    <>
      <div className="shrink-0 px-6 pt-6 pb-4">
        <DialogHeader>
          <DialogTitle>{editing ? 'Edit orchestrator' : 'New orchestrator'}</DialogTitle>
          <DialogDescription>
            A host-side AI session that drives and reviews work across agent environments. It
            delegates changes to a pod-backed environment&apos;s in-pod agent while reviewing that
            worktree on this machine read-only, and authors changes directly in a host
            environment&apos;s own directory, which no pod owns.
          </DialogDescription>
        </DialogHeader>
      </div>

      <div className="grid min-h-0 flex-1 gap-4 overflow-y-auto px-6 pb-1">
        <OrchestratorNameField name={name} onChange={form.setName} />
        <OrchestratorAliasField
          choices={form.aliasChoices}
          alias={form.alias}
          onChange={form.setAlias}
        />
        <OrchestratorLaunchFields launch={form.launch} disabled={busy} />
        <RestartRequiredNotice editing={editing} />
        <EnvironmentsField
          candidates={candidates}
          selected={selected}
          onToggle={toggle}
          onDirectoryChange={setDirectory}
          onRoleChange={setRole}
        />
        <DirectoriesField
          directories={directories}
          disabled={busy}
          onAdd={form.addDirectory}
          onRemove={form.removeDirectory}
        />
        {editing && !editing.transient ? (
          <OrchestratorGuidanceSection orchestratorId={editing.id} />
        ) : null}
        {error ? <InlineAlert>{error}</InlineAlert> : null}
      </div>

      <div className="shrink-0 border-t px-6 pt-4 pb-6">
        <DialogFooter className="sm:justify-between">
          {editing && !editing.transient ? (
            <OrchestratorManageActions
              editing={editing}
              busy={busy}
              onRequestDelete={onRequestDelete}
            />
          ) : (
            <span />
          )}
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                dispatch(closeOrchestratorDialog());
              }}
            >
              Cancel
            </Button>
            <Button
              type="button"
              disabled={busy || orchestratorScopeIsEmpty(selected, directories)}
              onClick={form.submit}
            >
              {busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
              {editing ? 'Save' : 'Create'}
            </Button>
          </div>
        </DialogFooter>
      </div>
    </>
  );
}

// OrchestratorNameField is optional; blank derives from the linked tenants.
function OrchestratorNameField({
  name,
  onChange,
}: {
  name: string;
  onChange: (name: string) => void;
}): React.ReactElement {
  return (
    <div className="space-y-1.5">
      <Label htmlFor="orchestrator-name">Name</Label>
      <Input
        id="orchestrator-name"
        value={name}
        placeholder="Optional — defaults from the tenants"
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
    </div>
  );
}

// OrchestratorAliasField picks from resolvable aliases only, since the writer refuses others.
function OrchestratorAliasField({
  choices,
  alias,
  onChange,
}: {
  choices: string[];
  alias: OrchestratorAlias;
  onChange: (alias: OrchestratorAlias) => void;
}): React.ReactElement {
  return (
    <SelectField
      id={ORCHESTRATOR_ALIAS_FIELD_ID}
      label="Platform alias"
      value={alias === '' ? ORCHESTRATOR_ALIAS_NONE : alias}
      options={orchestratorAliasOptions(choices, alias)}
      emptyLabel="No erun alias on this machine"
      helper={orchestratorAliasHelper(alias)}
      onChange={(value) => {
        onChange(orchestratorAliasValue(value));
      }}
    />
  );
}

// OrchestratorManageActions: restart (while running) and delete for a persisted orchestrator.
function OrchestratorManageActions({
  editing,
  busy,
  onRequestDelete,
}: {
  editing: OrchestratorInfo;
  busy: boolean;
  onRequestDelete: () => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const running = editing.status === 'running' && editing.sessionId > 0;
  return (
    <div className="flex gap-2">
      {running ? (
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={() => {
            dispatch(closeOrchestratorDialog());
            void dispatch(restartOrchestrator(editing.id));
          }}
        >
          <RotateCcw aria-hidden="true" />
          {restartNoticeText(editing) ? 'Restart to apply' : 'Restart'}
        </Button>
      ) : null}
      <Button
        type="button"
        variant="outline"
        disabled={busy}
        className="text-destructive hover:text-destructive"
        onClick={onRequestDelete}
      >
        <Trash2 aria-hidden="true" />
        Delete
      </Button>
    </div>
  );
}

// OrchestratorDeleteConfirm: one confirm step, since delete removes only the host-side definition.
function OrchestratorDeleteConfirm({
  editing,
  onCancel,
}: {
  editing: OrchestratorInfo;
  onCancel: () => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((state) => state.orchestrators.busy);
  const error = useAppSelector((state) => state.orchestrators.error);
  return (
    <>
      <DialogHeader>
        <DialogTitle>Delete orchestrator</DialogTitle>
        <DialogDescription>
          This removes the host-side definition. It cannot be undone.
        </DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-[18px_minmax(0,1fr)] items-start gap-[9px] rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_30%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_7%,transparent)] px-[11px] py-2.5 text-[13px] leading-[1.35] text-foreground">
        <AlertTriangle className="mt-px size-[17px] text-destructive" aria-hidden="true" />
        <span>
          Delete <span className="font-medium">{editing.name}</span>? The linked environments and
          their workspace sync are left intact.
        </span>
      </div>
      {error ? <InlineAlert>{error}</InlineAlert> : null}
      <DialogFooter>
        <Button type="button" variant="outline" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
        <Button
          type="button"
          variant="destructive"
          disabled={busy}
          onClick={() => {
            void dispatch(deleteOrchestrator(editing.id));
          }}
        >
          {busy ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <Trash2 aria-hidden="true" />
          )}
          Delete orchestrator
        </Button>
      </DialogFooter>
    </>
  );
}
