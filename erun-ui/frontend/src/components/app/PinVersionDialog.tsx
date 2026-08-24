import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  SelectField,
} from 'erun-kit';
import { LoaderCircle, Undo2 } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { applyPin, closePinVersion, previewPin, revertPin } from '@/app/pinVersionThunks';
import type { PinPlanView } from '@/app/slices/pinVersionSlice';
import { setPinTarget } from '@/app/slices/pinVersionSlice';

// The plan is always shown before anything is written. A re-pin rewrites files
// across the repo, so the operator should be agreeing to a specific set of
// changes rather than trusting that the right ones happen.
export function PinVersionDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const {
    open,
    selection,
    available,
    target,
    loadingVersions,
    previewing,
    applying,
    plan,
    applied,
    error,
    status,
  } = useAppSelector((state) => state.pinVersion);

  const busy = previewing || applying;
  const label = selection ? `${selection.tenant} / ${selection.environment}` : '';

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closePinVersion());
        }
      }}
    >
      <DialogContent className="sm:max-w-2xl" data-testid="pin-version-dialog">
        <DialogHeader>
          <DialogTitle>Change erun version</DialogTitle>
          <DialogDescription>
            Re-pins every erun reference for <span className="font-mono">{label}</span> together —
            the Terraform module refs, each umbrella chart’s erun dependencies, the build-env image
            tag, and the environment’s runtime version. Nothing is deployed: realizing the version
            stays a separate <span className="font-mono">terraform apply</span> and{' '}
            <span className="font-mono">deploy</span>.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <SelectField
            id="pin-version-target"
            label="Version"
            value={target}
            onChange={(next) => dispatch(setPinTarget(next))}
            disabled={loadingVersions || busy}
            options={[
              {
                value: '',
                label: loadingVersions ? 'Loading published versions…' : 'Latest stable',
              },
              ...available.map((version) => ({ value: version, label: version })),
            ]}
          />

          {plan ? <PinPlanTable plan={plan} applied={applied} /> : null}

          {error ? (
            <p role="alert" className="text-sm break-words text-destructive">
              {error}
            </p>
          ) : null}
          {status ? (
            <p role="status" className="text-sm break-words text-muted-foreground">
              {status}
            </p>
          ) : null}
        </div>

        <PinVersionFooter
          busy={busy}
          previewing={previewing}
          applying={applying}
          plan={plan}
          applied={applied}
        />
      </DialogContent>
    </Dialog>
  );
}

// The footer is where the decisions live, so it is its own component: Apply
// stays unavailable until a plan is on screen, because applying without one is
// exactly the blind motion this dialog exists to replace.
function PinVersionFooter({
  busy,
  previewing,
  applying,
  plan,
  applied,
}: {
  busy: boolean;
  previewing: boolean;
  applying: boolean;
  plan: PinPlanView | null;
  applied: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const canApply = !busy && plan !== null && !applied && !plan.aligned;
  return (
    <DialogFooter className="gap-2">
      <Button
        type="button"
        variant="outline"
        disabled={busy}
        aria-label="Revert to the previously pinned erun version"
        onClick={() => {
          void dispatch(revertPin());
        }}
      >
        <Undo2 aria-hidden="true" />
        Revert
      </Button>
      <Button
        type="button"
        variant="outline"
        disabled={busy}
        onClick={() => {
          void dispatch(previewPin());
        }}
      >
        {previewing ? <LoaderCircle className="size-4 animate-spin" aria-hidden="true" /> : null}
        Preview changes
      </Button>
      <Button
        type="button"
        disabled={!canApply}
        onClick={() => {
          void dispatch(applyPin());
        }}
      >
        {applying ? <LoaderCircle className="size-4 animate-spin" aria-hidden="true" /> : null}
        Apply
      </Button>
      <Button
        type="button"
        variant="ghost"
        onClick={() => {
          dispatch(closePinVersion());
        }}
      >
        Close
      </Button>
    </DialogFooter>
  );
}

function PinPlanTable({
  plan,
  applied,
}: {
  plan: PinPlanView;
  applied: boolean;
}): React.ReactElement {
  if (plan.aligned) {
    return (
      <p className="text-sm text-muted-foreground">
        Every reference is already on <span className="font-mono">{plan.target}</span> — nothing to
        change.
      </p>
    );
  }
  return (
    <div className="max-h-72 overflow-y-auto rounded-md border border-border/60">
      <table
        className="w-full text-sm"
        aria-label={applied ? 'Re-pinned references' : 'Pending pin changes'}
      >
        <tbody className="divide-y divide-border/60">
          {plan.sites.map((site) => (
            <tr key={`${site.kind}:${site.label}`} className={site.aligned ? 'opacity-50' : ''}>
              <td className="px-3 py-1.5 font-mono text-[11px] break-all">{site.label}</td>
              <td className="px-3 py-1.5 text-right whitespace-nowrap">
                <span className="font-mono text-muted-foreground">{site.current || '—'}</span>
                <span aria-hidden="true"> → </span>
                <span className="font-mono">{site.target}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
