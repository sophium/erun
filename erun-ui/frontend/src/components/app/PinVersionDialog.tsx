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
import { PIN_LATEST_STABLE_TARGET, setPinTarget } from '@/app/slices/pinVersionSlice';
import type { RootState } from '@/app/store';
import { InlineAlert } from '@/components/app/InlineAlert';
import type { UISelection } from '@/types';

import { PermissionNotice } from './InlineAlert';

// The fact the operator actually wants from the Version select is what the
// environment is on right now, not an abstract "no choice made" state
// (recognition over recall) — shown as helper text beside the control rather
// than baked into the "Latest stable" option, since the two can differ.
function selectPinCurrentVersion(state: RootState, selection: UISelection | null): string {
  if (!selection) {
    return '';
  }
  const tenant = state.tenants.tenants.find((item) => item.name === selection.tenant);
  const environment = tenant?.environments.find((item) => item.name === selection.environment);
  return environment?.runtimeVersion ?? '';
}

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
    checkoutResolvable,
    checkoutReason,
  } = useAppSelector((state) => state.pinVersion);
  const currentVersion = useAppSelector((state) => selectPinCurrentVersion(state, selection));

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
            the Terraform module refs, an erun image reference set directly in Terraform variables
            (e.g. the cluster-edge module’s <span className="font-mono">dns01_webhook_image</span>),
            each umbrella chart’s erun dependencies, the build-env image tag, a stated runtime chart
            or runtime image naming erun’s own stock release, and the environment’s own runtime
            version when that environment runs erun’s own runtime image. Nothing is deployed:
            realizing the version stays a separate{' '}
            <span className="font-mono">terraform apply</span> and{' '}
            <span className="font-mono">deploy</span>.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <PinCheckoutNotice resolvable={checkoutResolvable} reason={checkoutReason} />

          <SelectField
            id="pin-version-target"
            label="Version"
            value={target}
            onChange={(next) => dispatch(setPinTarget(next))}
            disabled={loadingVersions || busy}
            helper={currentVersion ? `Currently pinned to ${currentVersion}.` : undefined}
            options={[
              {
                value: PIN_LATEST_STABLE_TARGET,
                label: loadingVersions ? 'Loading published versions…' : 'Latest stable',
              },
              ...available.map((version) => ({ value: version, label: version })),
            ]}
          />

          {plan ? <PinPlanTable plan={plan} applied={applied} /> : null}

          {error ? <InlineAlert>{error}</InlineAlert> : null}
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
          checkoutResolvable={checkoutResolvable}
        />
      </DialogContent>
    </Dialog>
  );
}

// A sourceless environment has no local checkout for pin to rewrite, so this
// says so up front rather than letting Preview/Apply fail after a click.
function PinCheckoutNotice({
  resolvable,
  reason,
}: {
  resolvable: boolean;
  reason: string;
}): React.ReactElement | null {
  if (resolvable) {
    return null;
  }
  return (
    <PermissionNotice>
      {reason ||
        'This environment has no local checkout of its repo on this machine, so its pins cannot be previewed or applied from here.'}
    </PermissionNotice>
  );
}

// The footer is where the decisions live, so it is its own component: Apply
// stays unavailable until a plan is on screen, because applying without one is
// exactly the blind motion this dialog exists to replace. Preview, Apply and
// Revert all rewrite files in a local checkout, so all three stay unavailable
// when checkoutResolvable is false — there is no checkout on this machine for
// any of them to act on, and offering the buttons would just move the dead
// end from "up front" to "after a click".
function PinVersionFooter({
  busy,
  previewing,
  applying,
  plan,
  applied,
  checkoutResolvable,
}: {
  busy: boolean;
  previewing: boolean;
  applying: boolean;
  plan: PinPlanView | null;
  applied: boolean;
  checkoutResolvable: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const blocked = busy || !checkoutResolvable;
  const canApply = !blocked && plan !== null && !applied && !plan.aligned;
  return (
    <DialogFooter className="gap-2">
      <Button
        type="button"
        variant="outline"
        disabled={blocked}
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
        disabled={blocked}
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
  return (
    <>
      {plan.aligned ? (
        <p className="text-sm text-muted-foreground">
          Every reference is already on <span className="font-mono">{plan.target}</span> — nothing
          to change.
        </p>
      ) : (
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
      )}
      <PinSkippedNotes notes={plan.skipped} />
    </>
  );
}

// PinSkippedNotes reports what a re-pin deliberately did not touch and why. A
// repin site can be absent from the plan because it is already aligned or
// because the environment's own config puts it on another release line, and the
// operator has to be able to tell those apart: an environment that runs its own
// runtime image keeps its runtimeversion, and the plan says so rather than
// leaving a row they expected simply missing.
function PinSkippedNotes({ notes }: { notes?: string[] }): React.ReactElement | null {
  if (!notes?.length) {
    return null;
  }
  return (
    <div className="mt-2 text-xs text-muted-foreground" data-testid="pin-skipped">
      <p className="font-medium">Left alone:</p>
      <ul className="mt-1 list-disc space-y-1 pl-4">
        {notes.map((note) => (
          <li key={note}>{note}</li>
        ))}
      </ul>
    </div>
  );
}
