import { Download, FileText, Folder, LoaderCircle, Play } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { downloadOutput, runOutputOnHost } from '@/app/outputsThunks';
import { closeOutputsDialog } from '@/app/slices/outputsDialogSlice';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import type { AgentOutputEntry } from '@/types';

// OutputsDialog is the deliverables counterpart to the activity queue (which
// tracks operations): the surface for the output files an agent left behind.
export function OutputsDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const {
    open,
    loading,
    error,
    dir,
    entries,
    downloadingName,
    runningName,
    status,
    statusError,
    target,
  } = useAppSelector((state) => state.outputsDialog);
  const orchestrator = target?.kind === 'orchestrator';
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeOutputsDialog());
        }
      }}
    >
      <DialogContent className="sm:max-w-2xl" data-testid="outputs-dialog">
        <DialogHeader>
          <DialogTitle>
            {target?.kind === 'orchestrator' ? `Outputs — ${target.name}` : 'Agent outputs'}
          </DialogTitle>
          <DialogDescription>
            {orchestrator
              ? 'Files this orchestrator produced on this machine'
              : 'Files an agent produced in this environment’s runtime pod'}
            {dir ? <span className="font-mono"> ({dir})</span> : null}. Download saves each
            somewhere of your choosing; folders download as a .tar.gz archive.{' '}
            {orchestrator
              ? 'They were produced here, so runnable ones run in place.'
              : 'Host-runnable binaries can be run on this machine from the copy workspace sync mirrors down.'}
          </DialogDescription>
        </DialogHeader>
        <OutputsDialogBody
          loading={loading}
          error={error}
          entries={entries}
          downloadingName={downloadingName}
          runningName={runningName}
          hostNative={orchestrator}
        />
        {status ? (
          <p
            role={statusError ? 'alert' : 'status'}
            className={
              statusError
                ? 'text-sm break-words text-destructive'
                : 'text-sm break-words text-muted-foreground'
            }
          >
            {status}
          </p>
        ) : null}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              dispatch(closeOutputsDialog());
            }}
          >
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function OutputsDialogBody({
  loading,
  error,
  entries,
  downloadingName,
  runningName,
  hostNative,
}: {
  loading: boolean;
  error: string;
  entries: AgentOutputEntry[];
  downloadingName: string;
  runningName: string;
  hostNative: boolean;
}): React.ReactElement {
  if (loading) {
    return (
      <div className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
        <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
        Listing outputs…
      </div>
    );
  }
  if (error) {
    return (
      <p role="alert" className="py-4 text-sm break-words text-destructive">
        {error}
      </p>
    );
  }
  if (entries.length === 0) {
    return (
      <p className="py-4 text-sm text-muted-foreground">
        No outputs yet. Files written to the agent’s $ERUN_OUTPUTS_DIR show up here.
      </p>
    );
  }
  return (
    <div className="max-h-80 overflow-y-auto">
      <ul className="divide-y divide-border/60" aria-label="Agent outputs">
        {entries.map((entry) => (
          <OutputRow
            key={entry.name}
            entry={entry}
            downloading={downloadingName === entry.name}
            running={runningName === entry.name}
            anyBusy={downloadingName !== '' || runningName !== ''}
            hostNative={hostNative}
          />
        ))}
      </ul>
    </div>
  );
}

function OutputRow({
  entry,
  downloading,
  running,
  anyBusy,
  hostNative,
}: {
  entry: AgentOutputEntry;
  downloading: boolean;
  running: boolean;
  anyBusy: boolean;
  hostNative: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const Icon = entry.isDir ? Folder : FileText;
  const runnable = !entry.isDir && isHostRunnableArtifact(entry.name, hostNative);
  return (
    <li className="flex items-center gap-3 py-2.5">
      <Icon className="size-4 flex-none text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium text-foreground">{entry.name}</div>
        <div className="text-[11px] text-muted-foreground">
          {formatOutputSize(entry.size)} · {entry.isDir ? 'folder' : 'file'} ·{' '}
          {formatRelativeTime(entry.modTime)}
        </div>
      </div>
      {runnable ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={anyBusy}
          aria-label={`Run ${entry.name} on this machine`}
          onClick={() => {
            void dispatch(runOutputOnHost(entry.name));
          }}
        >
          {running ? (
            <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
          ) : (
            <Play aria-hidden="true" />
          )}
          Run on host
        </Button>
      ) : null}
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={anyBusy}
        aria-label={`Download ${entry.name}`}
        onClick={() => {
          void dispatch(downloadOutput(entry.name));
        }}
      >
        {downloading ? (
          <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
        ) : (
          <Download aria-hidden="true" />
        )}
        Download
      </Button>
    </li>
  );
}

// isHostRunnableArtifact reports whether an artifact is a binary the host can
// launch directly. For a pod-produced artifact that means Windows executable
// suffixes — the primary case is a cross-built .exe from the Linux pod — so the
// action stays inert for report/data outputs and on hosts that produce none.
//
// An orchestrator's outputs were produced on this host, so a native binary there
// usually carries no suffix at all; requiring one would leave the action dead for
// exactly the case it exists to serve. Data suffixes are excluded instead, which
// is the safer direction to be wrong in: an unrunnable file simply fails to
// launch, whereas a hidden button cannot be recovered from.
function isHostRunnableArtifact(name: string, hostNative: boolean): boolean {
  const lower = name.toLowerCase();
  if (lower.endsWith('.exe') || lower.endsWith('.bat') || lower.endsWith('.cmd')) {
    return true;
  }
  if (!hostNative) {
    return false;
  }
  return !NON_RUNNABLE_OUTPUT_SUFFIXES.some((suffix) => lower.endsWith(suffix));
}

const NON_RUNNABLE_OUTPUT_SUFFIXES = [
  '.md',
  '.txt',
  '.json',
  '.yaml',
  '.yml',
  '.csv',
  '.log',
  '.html',
  '.png',
  '.jpg',
  '.jpeg',
  '.svg',
  '.pdf',
  '.zip',
  '.tar',
  '.gz',
  '.patch',
  '.diff',
];

function formatOutputSize(size: number): string {
  if (size < 1024) {
    return `${String(size)} B`;
  }
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = size / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit] ?? 'KB'}`;
}

function formatRelativeTime(iso: string): string {
  const when = new Date(iso).getTime();
  if (Number.isNaN(when)) {
    return 'unknown';
  }
  const seconds = Math.round((Date.now() - when) / 1000);
  if (seconds < 60) {
    return 'just now';
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `${String(minutes)}m ago`;
  }
  const hours = Math.round(minutes / 60);
  if (hours < 24) {
    return `${String(hours)}h ago`;
  }
  const days = Math.round(hours / 24);
  return `${String(days)}d ago`;
}
