import { Download, FileText, Folder, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { downloadOutput } from '@/app/outputsThunks';
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
  const { open, loading, error, dir, entries, downloadingName, status, statusError } =
    useAppSelector((state) => state.outputsDialog);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeOutputsDialog());
        }
      }}
    >
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Agent outputs</DialogTitle>
          <DialogDescription>
            Files an agent produced in this environment’s runtime pod
            {dir ? <span className="font-mono"> ({dir})</span> : null}. Download pulls each onto
            this machine; folders download as a .tar.gz archive.
          </DialogDescription>
        </DialogHeader>
        <OutputsDialogBody
          loading={loading}
          error={error}
          entries={entries}
          downloadingName={downloadingName}
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
}: {
  loading: boolean;
  error: string;
  entries: AgentOutputEntry[];
  downloadingName: string;
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
        No outputs yet. Files an agent or skill writes to the pod’s outputs directory show up here.
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
            anyDownloading={downloadingName !== ''}
          />
        ))}
      </ul>
    </div>
  );
}

function OutputRow({
  entry,
  downloading,
  anyDownloading,
}: {
  entry: AgentOutputEntry;
  downloading: boolean;
  anyDownloading: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const Icon = entry.isDir ? Folder : FileText;
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
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={anyDownloading}
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
