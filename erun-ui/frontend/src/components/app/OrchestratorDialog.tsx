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
  type OrchestratorEnvRef,
  type OrchestratorEnvRole,
  type OrchestratorInfo,
} from '@/app/slices/orchestratorsSlice';
import { OrchestratorConversationsSection } from '@/components/app/OrchestratorDialog.Conversations';
import { EnvironmentsField } from '@/components/app/OrchestratorDialog.Environments';
import {
  type EnvCandidate,
  envKey,
} from '@/components/app/OrchestratorDialog.Environments.helpers';
import { OrchestratorGuidanceSection } from '@/components/app/OrchestratorDialog.Guidance';

import { ListOrchestratorEnvCandidates } from '../../../wailsjs/go/main/App';

// OrchestratorDialog creates or edits a persisted orchestrator: a name and the
// agent environments it links, each with the host directory it reviews read-only.
// Creating prepares that directory — the mirror plus its one-way sync for a
// remote-agent env, nothing for a local-agent env whose worktree is already here;
// editing re-links the current set.
interface OrchestratorForm {
  candidates: EnvCandidate[];
  name: string;
  setName: (name: string) => void;
  selected: OrchestratorEnvRef[];
  toggle: (candidate: EnvCandidate, checked: boolean) => void;
  setDirectory: (ref: OrchestratorEnvRef, directory: string) => void;
  setRole: (ref: OrchestratorEnvRef, role: OrchestratorEnvRole) => void;
}

function useOrchestratorForm(open: boolean, editing: OrchestratorInfo | null): OrchestratorForm {
  const [candidates, setCandidates] = React.useState<EnvCandidate[]>([]);
  const [name, setName] = React.useState('');
  const [selected, setSelected] = React.useState<OrchestratorEnvRef[]>([]);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    setName(editing?.name ?? '');
    setSelected(editing ? editing.environments.map((env) => ({ ...env })) : []);
    void ListOrchestratorEnvCandidates().then((list) => {
      // The Wails binding types requiredRole as a plain string (Go's
      // OrchestratorEnvRole erases to that on the wire); loadOrchestrators
      // takes the same widen-then-narrow approach for OrchestratorInfo below.
      setCandidates(list as EnvCandidate[]);
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
              // Not declared, never a silent default of either known role —
              // an operator who wants one picks it explicitly. The one
              // exception is a candidate with a requiredRole (a runtime
              // environment): it has exactly one legal choice, so pre-selecting
              // it is not guessing a default, it is the only value the picker
              // will actually accept — see EnvironmentRow's own role options.
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

  return { candidates, name, setName, selected, toggle, setDirectory, setRole };
}

// OrchestratorDialog is the single management surface for an orchestrator,
// mirroring the environment Manage dialog: the row carries only a "…" that
// opens this, and restart + delete (with confirmation) live here rather than as
// inline row buttons. It swaps to a delete-confirmation view in place.
export function OrchestratorDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const open = useAppSelector((state) => state.orchestrators.dialogOpen);
  const editing = useAppSelector((state) => state.orchestrators.editing);
  const [confirmingDelete, setConfirmingDelete] = React.useState(false);

  // Leaving the dialog always resets it, so reopening (for this or another
  // orchestrator) starts on the edit form, never mid-confirmation.
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
      <DialogContent className="sm:max-w-lg">
        {editing && confirmingDelete ? (
          <OrchestratorDeleteConfirm
            editing={editing}
            onCancel={() => {
              setConfirmingDelete(false);
            }}
          />
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
  const { candidates, name, setName, selected, toggle, setDirectory, setRole } =
    useOrchestratorForm(open, editing);
  const submit = (): void => {
    if (editing) {
      void dispatch(updateOrchestrator(editing.id, name, selected));
    } else {
      void dispatch(createOrchestrator(name, selected));
    }
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>{editing ? 'Edit orchestrator' : 'New orchestrator'}</DialogTitle>
        <DialogDescription>
          A host-side AI session that drives and reviews work across agent environments. It reads
          each linked environment&apos;s code on this machine, read-only, and delegates every change
          to the in-pod agents.
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="orchestrator-name">Name</Label>
          <Input
            id="orchestrator-name"
            value={name}
            placeholder="Optional — defaults from the tenants"
            onChange={(event) => {
              setName(event.target.value);
            }}
          />
        </div>
        <RestartRequiredNotice editing={editing} />
        <EnvironmentsField
          candidates={candidates}
          selected={selected}
          onToggle={toggle}
          onDirectoryChange={setDirectory}
          onRoleChange={setRole}
        />
        {editing && !editing.transient ? (
          <>
            <OrchestratorConversationsSection orchestratorId={editing.id} />
            <OrchestratorGuidanceSection orchestratorId={editing.id} />
          </>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm break-words text-destructive">
            {error}
          </p>
        ) : null}
      </div>

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
          <Button type="button" disabled={busy || selected.length === 0} onClick={submit}>
            {busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
            {editing ? 'Save' : 'Create'}
          </Button>
        </div>
      </DialogFooter>
    </>
  );
}

// OrchestratorManageActions is the footer's left cluster for an existing
// orchestrator: restart (only while its session is running) and delete. Split
// out so the form stays within the lint size/complexity budgets and so
// `editing` arrives already narrowed to a persisted definition.
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
          {editing.restartRequired || editing.roleChanged ? 'Restart to apply' : 'Restart'}
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

// restartNoticeText picks the accurate reason for the amber notice below.
// restartRequired (an environment link added or removed) always takes the
// tools-specific wording, since it is the one guaranteed true; a role-only
// edit (roleChanged) gets its own honest wording instead of being folded into
// the same sentence — a role never changes which MCP tools the session holds,
// so claiming "tools are missing" for it would be a false diagnosis (root
// AGENTS.md § "Smooth, Seamless, No Dead Ends": distinguish causes before
// writing copy).
function restartNoticeText(editing: OrchestratorInfo): string | null {
  if (editing.restartRequired) {
    return 'Its environments changed while it was running. The session still holds tools for what it was linked to before and none for what was added since.';
  }
  if (editing.roleChanged) {
    return "A linked environment's role changed while it was running. It's saved, but the session's current context may still reflect the old one — restart to be sure it picks up the change.";
  }
  return null;
}

// RestartRequiredNotice tells the operator, at the top of the form they just
// opened, that their earlier edit did not take effect yet: a live Claude Code
// session resolves its MCP toolset once at launch, so it still holds tools for
// whatever it was linked to before and none for what was added since — only a
// restart re-wires it (erun#1319). It carries the exact control that resolves
// it (root AGENTS.md: "a message naming a remedy should carry the action that
// resolves it") rather than pointing at the footer's Restart button below.
// Takes the raw `editing` value and decides for itself whether there is
// anything to say, so the caller stays a plain, unconditional render.
function RestartRequiredNotice({
  editing,
}: {
  editing: OrchestratorInfo | null;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((state) => state.orchestrators.busy);
  const text = editing?.status === 'running' ? restartNoticeText(editing) : null;
  if (!editing || !text) {
    return null;
  }
  const orchestratorId = editing.id;
  return (
    <div
      role="status"
      className="grid gap-1 rounded-[var(--radius)] border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-[12px] leading-[1.4] text-foreground"
    >
      <span className="flex items-start gap-2">
        <AlertTriangle aria-hidden="true" className="mt-[1px] size-3.5 shrink-0" />
        <span className="grid gap-1">
          <span>{text}</span>
          <span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => {
                dispatch(closeOrchestratorDialog());
                void dispatch(restartOrchestrator(orchestratorId));
              }}
            >
              <RotateCcw aria-hidden="true" />
              Restart now
            </Button>
          </span>
        </span>
      </span>
    </div>
  );
}

// OrchestratorDeleteConfirm mirrors the environment delete's explicit-confirm
// gate. Orchestrator delete is far less destructive than env delete (it removes
// only the host-side definition; the linked environments and their workspace
// sync survive), so a single confirm step is the right friction — no typed name.
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
      {error ? (
        <p role="alert" className="text-sm break-words text-destructive">
          {error}
        </p>
      ) : null}
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
