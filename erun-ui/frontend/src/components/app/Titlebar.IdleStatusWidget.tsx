import {
  Button,
  cn,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  IconTooltip,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from 'erun-kit';
import { LoaderCircle, Lock, Play, Power, Unlock, X } from 'lucide-react';
import * as React from 'react';

import {
  useDisableCloudContextApiStopMutation,
  useEnableCloudContextApiStopMutation,
  useGetCloudContextApiStopQuery,
} from '@/app/api/cloudApi';
import { cloudNodeOperationFor } from '@/app/cloudNodeOperations';
import { readError } from '@/app/errors';
import { toggleIdleCloudContext } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { displayableIdleStatus } from '@/app/idleStatusEligibility';
import { cancelPendingIdleStop } from '@/app/idleThunks';
import { showNotification } from '@/app/notificationThunks';
import {
  formatGraceCountdown,
  idleCloudAction,
  type IdleStatus,
  idleStatusAccessibleLabel,
  idleStatusBadge,
  idleStatusTooltipLines,
  idleStopPending,
} from '@/components/app/Titlebar.helpers';

function useIdleWidgetState(): {
  idleStatus: IdleStatus | null;
  idleBadge: { label: string; className: string } | null;
  idleAction: ReturnType<typeof idleCloudAction>;
  cloudContextName: string;
} | null {
  const rawIdleStatus = useAppSelector((state) => state.idle.idleStatus);
  const idleStatus = displayableIdleStatus(rawIdleStatus);
  const cloudContextName = rawIdleStatus?.cloudContextName?.trim() ?? '';
  // Scoped to the node this widget actually names. The name follows the
  // selected environment, so a flag that did not would pair one environment's
  // node with another's operation — the cross-environment bleed that let a
  // progressive label survive onto a row with nothing running.
  const inFlight = useAppSelector((state) =>
    cloudNodeOperationFor(state.idle.cloudNodeOperations, cloudContextName),
  );
  const idleAction = rawIdleStatus ? idleCloudAction(rawIdleStatus, inFlight) : null;
  if (!idleStatus && !idleAction) {
    return null;
  }
  const idleBadge = idleStatus ? idleStatusBadge(idleStatus) : null;
  return { idleStatus, idleBadge, idleAction, cloudContextName };
}

function renderIdleLeadingBadge(
  busy: boolean,
  idleAction: ReturnType<typeof idleCloudAction>,
  idleStatus: IdleStatus | null,
  idleBadge: { label: string; className: string } | null,
  hasAction: boolean,
  cloudContextName: string,
): React.ReactElement | null {
  if (idleStatus && idleStopPending(idleStatus) && !busy) {
    return (
      <IdleStopWarningBadge
        idleStatus={idleStatus}
        cloudContextName={cloudContextName}
        hasAction={hasAction}
      />
    );
  }
  if (busy && idleAction) {
    return <IdleTransitionBadge label={idleAction.label} hasAction={hasAction} />;
  }
  if (idleStatus && idleBadge) {
    return <IdleStatusBadge idleStatus={idleStatus} idleBadge={idleBadge} hasAction={hasAction} />;
  }
  return null;
}

function pickEnvDisplayName(idleStatus: IdleStatus, fallback: string): string {
  const label = (idleStatus.cloudContextLabel ?? '').trim();
  if (label) return label;
  const name = (idleStatus.cloudContextName ?? '').trim();
  if (name) return name;
  return fallback;
}

// The Cancel button dismisses the pending-stop grace window locally; it does
// not touch AWS state.
function IdleStopWarningBadge({
  idleStatus,
  cloudContextName,
  hasAction,
}: {
  idleStatus: IdleStatus;
  cloudContextName: string;
  hasAction: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const selected = useAppSelector((state) => state.selection.selected);
  const [busy, setBusy] = React.useState(false);
  const secondsRemaining = Math.max(0, idleStatus.secondsUntilForcedStop ?? 0);
  const countdown = formatGraceCountdown(secondsRemaining);
  const grace = idleStatus.gracePeriodSeconds ?? 0;
  const displayName = pickEnvDisplayName(idleStatus, cloudContextName);
  const cancelLabel = `Cancel pending auto-stop for ${displayName}`;
  const handleCancel = () => {
    if (busy || !selected) {
      return;
    }
    setBusy(true);
    void dispatch(cancelPendingIdleStop(selected)).finally(() => {
      setBusy(false);
    });
  };
  return (
    <>
      <div
        className={cn(
          'flex h-full items-center px-2 font-mono text-[12px] leading-none text-amber-600',
          'border-r',
        )}
        role="status"
        aria-live="polite"
        aria-label={`Auto-stop ${countdown}. Grace period ${String(grace)} seconds.`}
        data-testid="titlebar-idle-stop-warning"
      >
        Auto-stop {countdown}
      </div>
      <IconTooltip label={cancelLabel}>
        <Button
          className={cn(
            'h-full w-7 rounded-none border-0 bg-transparent text-amber-600 hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-60 [&_svg]:size-3.5',
            !hasAction && 'rounded-r-md',
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label={cancelLabel}
          disabled={busy}
          onClick={handleCancel}
          data-testid="titlebar-idle-stop-cancel"
        >
          <X aria-hidden="true" />
        </Button>
      </IconTooltip>
    </>
  );
}

function idleWidgetContainerClass(
  busy: boolean,
  pending: boolean,
  idleBadge: { label: string; className: string } | null,
): string | undefined {
  if (busy) return undefined;
  if (pending) return 'border-amber-600/70';
  return idleBadge?.className;
}

export function IdleStatusWidget(): React.ReactElement | null {
  const state = useIdleWidgetState();
  if (!state) {
    return null;
  }
  const { idleStatus, idleBadge, idleAction, cloudContextName } = state;
  const hasAction = Boolean(idleAction);
  const hasFollowOn = Boolean(cloudContextName);
  const busy = Boolean(idleAction?.busy);
  const pending = Boolean(idleStatus && idleStopPending(idleStatus));
  return (
    <div
      className={cn(
        'flex h-7 items-center rounded-md border bg-background',
        idleWidgetContainerClass(busy, pending, idleBadge),
      )}
    >
      {renderIdleLeadingBadge(busy, idleAction, idleStatus, idleBadge, hasAction, cloudContextName)}
      {idleAction ? (
        <IdleStatusAction
          idleAction={idleAction}
          hasBadge={busy || pending || Boolean(idleStatus)}
          hasFollowOn={hasFollowOn}
        />
      ) : null}
      {cloudContextName ? <StopProtectionToggle contextName={cloudContextName} /> : null}
    </div>
  );
}

function IdleTransitionBadge({
  label,
  hasAction,
}: {
  label: string;
  hasAction: boolean;
}): React.ReactElement {
  return (
    <div
      className={cn(
        'flex h-full min-w-[64px] items-center justify-center px-2 font-mono text-[12px] leading-none text-muted-foreground',
        hasAction && 'border-r',
      )}
      role="status"
      aria-live="polite"
      data-testid="titlebar-idle-transition"
    >
      {label}…
    </div>
  );
}

function IdleStatusBadge({
  idleStatus,
  idleBadge,
  hasAction,
}: {
  idleStatus: IdleStatus;
  idleBadge: { label: string; className: string };
  hasAction: boolean;
}): React.ReactElement {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          className={cn(
            'flex h-full min-w-[64px] cursor-default items-center justify-center rounded-l-md border-0 bg-transparent px-2 font-mono text-[12px] leading-none outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring',
            hasAction && 'border-r',
            idleBadge.className,
          )}
          aria-label={idleStatusAccessibleLabel(idleStatus)}
        >
          {idleBadge.label}
        </button>
      </TooltipTrigger>
      <TooltipContent
        side="bottom"
        align="end"
        className="max-w-[360px] whitespace-normal text-left leading-5"
      >
        <div className="space-y-1">
          {idleStatusTooltipLines(idleStatus).map((line, index) => (
            <div key={`${String(index)}-${line}`} className={tooltipLineClassName(line)}>
              {line.replace(/^ +/, '')}
            </div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

function idleStatusActionShape(
  busy: boolean,
  action: 'start' | 'stop',
  hasBadge: boolean,
  hasFollowOn: boolean,
): { Icon: typeof LoaderCircle; shape: string } {
  const Icon = busy ? LoaderCircle : action === 'start' ? Play : Power;
  const key = `${hasBadge ? 'b' : ''}${hasFollowOn ? 'f' : ''}`;
  const shapes: Record<string, string> = {
    '': 'rounded-md',
    b: 'rounded-l-none rounded-r-md',
    f: 'rounded-l-md rounded-r-none border-r',
    bf: 'rounded-none border-r',
  };
  return { Icon, shape: shapes[key] ?? 'rounded-md' };
}

function IdleStatusAction({
  idleAction,
  hasBadge,
  hasFollowOn,
}: {
  idleAction: { action: 'start' | 'stop'; label: string; busy: boolean };
  hasBadge: boolean;
  hasFollowOn: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const { Icon, shape } = idleStatusActionShape(
    idleAction.busy,
    idleAction.action,
    hasBadge,
    hasFollowOn,
  );
  return (
    <IconTooltip label={idleAction.label}>
      <Button
        className={cn(
          'h-full w-7 border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-60 [&_svg]:size-3.5',
          shape,
        )}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={idleAction.label}
        disabled={idleAction.busy}
        onClick={() => {
          void dispatch(toggleIdleCloudContext());
        }}
      >
        <Icon className={cn(idleAction.busy && 'animate-spin')} aria-hidden="true" />
      </Button>
    </IconTooltip>
  );
}

// Click is deliberately NOT gated on the in-flight describe call: a hung
// describe (idle-stopped instance, expired SSO token) would otherwise leave
// the button disabled forever. It stays interactive while the state is
// unknown, and the mutation reports its own outcome.
function useStopProtectionState(contextName: string): {
  Icon: typeof LoaderCircle;
  label: string;
  amber: boolean;
  pressed: boolean;
  busy: boolean;
  locked: boolean;
} {
  const { data } = useGetCloudContextApiStopQuery(contextName, {
    refetchOnMountOrArgChange: true,
  });
  const [, disableMeta] = useDisableCloudContextApiStopMutation({
    fixedCacheKey: `cloud-context-disable-api-stop:${contextName}`,
  });
  const [, enableMeta] = useEnableCloudContextApiStopMutation({
    fixedCacheKey: `cloud-context-enable-api-stop:${contextName}`,
  });
  const known = Boolean(data?.stopProtectionKnown);
  const locked = Boolean(data?.stopProtection);
  const busy = disableMeta.isLoading || enableMeta.isLoading;
  const Icon = busy ? LoaderCircle : locked ? Lock : Unlock;
  const label = locked
    ? `Unlock ${contextName}: re-enable auto-stop and manual Stop`
    : `Lock ${contextName}: block every stop (including the in-pod idle monitor and your own Stop button) until you unlock`;
  return { Icon, label, amber: known && locked, pressed: known && locked, busy, locked };
}

function useStopProtectionMutators(contextName: string): {
  disable: () => Promise<void>;
  enable: () => Promise<void>;
} {
  const dispatch = useAppDispatch();
  const [disableApiStop] = useDisableCloudContextApiStopMutation({
    fixedCacheKey: `cloud-context-disable-api-stop:${contextName}`,
  });
  const [enableApiStop] = useEnableCloudContextApiStopMutation({
    fixedCacheKey: `cloud-context-enable-api-stop:${contextName}`,
  });
  return {
    disable: async () => {
      try {
        await disableApiStop(contextName).unwrap();
        dispatch(
          showNotification(
            'success',
            `${contextName}: locked (auto-stop and manual Stop disabled)`,
          ),
        );
      } catch (error) {
        dispatch(showNotification('error', `Lock ${contextName} failed: ${readError(error)}`));
      }
    },
    enable: async () => {
      try {
        await enableApiStop(contextName).unwrap();
        dispatch(showNotification('success', `${contextName}: unlocked (auto-stop restored)`));
      } catch (error) {
        dispatch(showNotification('error', `Unlock ${contextName} failed: ${readError(error)}`));
      }
    },
  };
}

// Only the lock direction is confirmed: it is destructive because it also
// blocks the user's own manual Stop until reversed. Unlocking is one-click.
function StopProtectionConfirmDialog({
  open,
  contextName,
  busy,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  contextName: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}): React.ReactElement {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !busy) {
          onCancel();
        }
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Lock {contextName}?</DialogTitle>
          <DialogDescription>
            While locked, AWS rejects every <code>ec2:StopInstances</code> call. The in-pod idle
            monitor, your own Stop button, and any external script will all fail to stop the
            instance until you unlock it. Use this to keep an unhealthy env up while you repair it —
            and remember to unlock when you&apos;re done so the env can auto-stop again.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="gap-2 sm:gap-2">
          <Button type="button" variant="outline" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
          <Button type="button" disabled={busy} onClick={onConfirm}>
            {busy ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <Lock aria-hidden="true" />
            )}
            Lock {contextName}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// Locking is the recovery lever for when the in-pod idle monitor races a
// repair (e.g. erun-mcp stuck in ImagePullBackOff reads as idle, so the
// monitor keeps stopping the instance). While locked, AWS rejects every stop
// — including the user's own adjacent Power button — until the user clears it.
function StopProtectionToggle({ contextName }: { contextName: string }): React.ReactElement {
  const { Icon, label, amber, pressed, busy, locked } = useStopProtectionState(contextName);
  const { disable, enable } = useStopProtectionMutators(contextName);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const onClick = () => {
    if (busy) {
      return;
    }
    if (locked) {
      void enable();
      return;
    }
    setConfirmOpen(true);
  };
  return (
    <>
      <IconTooltip label={label}>
        <Button
          className={cn(
            'h-full w-7 rounded-l-none rounded-r-md border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-60 [&_svg]:size-3.5',
            amber && 'text-amber-600',
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label={label}
          aria-pressed={pressed}
          disabled={busy}
          onClick={onClick}
        >
          <Icon className={cn(busy && 'animate-spin')} aria-hidden="true" />
        </Button>
      </IconTooltip>
      <StopProtectionConfirmDialog
        open={confirmOpen}
        contextName={contextName}
        busy={busy}
        onCancel={() => {
          setConfirmOpen(false);
        }}
        onConfirm={() => {
          setConfirmOpen(false);
          void disable();
        }}
      />
    </>
  );
}

// idleStatusTooltipLines encodes nesting depth as leading spaces, but HTML
// collapses that whitespace, so re-encode the "- "/"  - " depth as padding.
function tooltipLineClassName(line: string): string | undefined {
  if (line.startsWith('  - ')) {
    return 'pl-6';
  }
  if (line.startsWith('- ')) {
    return 'pl-2';
  }
  return undefined;
}
