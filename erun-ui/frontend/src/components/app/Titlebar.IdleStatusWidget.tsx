import { LoaderCircle, Lock, Play, Power, Unlock, X } from 'lucide-react';
import * as React from 'react';

import {
  useDisableCloudContextApiStopMutation,
  useEnableCloudContextApiStopMutation,
  useGetCloudContextApiStopQuery,
} from '@/app/api/cloudApi';
import { readError } from '@/app/errors';
import { toggleIdleCloudContext } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { displayableIdleStatus } from '@/app/idleStatusEligibility';
import { cancelPendingIdleStop } from '@/app/idleThunks';
import { showNotification } from '@/app/notificationThunks';
import { IconTooltip } from '@/components/app/IconTooltip';
import {
  formatGraceCountdown,
  idleCloudAction,
  type IdleStatus,
  idleStatusAccessibleLabel,
  idleStatusBadge,
  idleStatusTooltipLines,
  idleStopPending,
} from '@/components/app/Titlebar.helpers';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

// useIdleWidgetState derives the titlebar's render-time view from the
// redux idle state, returning null when the widget should render nothing.
function useIdleWidgetState(): {
  idleStatus: IdleStatus | null;
  idleBadge: { label: string; className: string } | null;
  idleAction: ReturnType<typeof idleCloudAction>;
  cloudContextName: string;
} | null {
  const rawIdleStatus = useAppSelector((state) => state.idle.idleStatus);
  const idleCloudContextBusy = useAppSelector((state) => state.idle.idleCloudContextBusy);
  const idleStatus = displayableIdleStatus(rawIdleStatus);
  const idleAction = rawIdleStatus ? idleCloudAction(rawIdleStatus, idleCloudContextBusy) : null;
  const cloudContextName = rawIdleStatus?.cloudContextName?.trim() ?? '';
  if (!idleStatus && !idleAction) {
    return null;
  }
  const idleBadge = idleStatus ? idleStatusBadge(idleStatus) : null;
  return { idleStatus, idleBadge, idleAction, cloudContextName };
}

// renderIdleLeadingBadge chooses the leading pill for the current
// state: the warning pill when auto-stop is armed, the transition pill
// during a stop/start, the idle-status pill otherwise, or nothing.
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

// pickEnvDisplayName chooses the first non-empty display string for
// the env, preferring the human-friendly kube context label over the
// raw context name.
function pickEnvDisplayName(idleStatus: IdleStatus, fallback: string): string {
  const label = (idleStatus.cloudContextLabel ?? '').trim();
  if (label) return label;
  const name = (idleStatus.cloudContextName ?? '').trim();
  if (name) return name;
  return fallback;
}

// IdleStopWarningBadge is the amber "auto-stop is armed" pill that
// replaces the idle-time pill once `stopPendingSince` is set. It
// surfaces the countdown to forced stop and a Cancel button that
// dismisses the grace window without touching AWS state.
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

// idleWidgetContainerClass returns the container's tailwind classes
// for the current busy/pending/idle widget state.
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

// idleStatusActionShape returns the icon plus the rounding/border that
// depend on whether the action button has a badge to its left and/or
// another button to its right.
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

// useStopProtectionState centralises the redux-toolkit-query plumbing
// and returns the view StopProtectionToggle renders against.
//
// Click is deliberately NOT gated on the in-flight describe call.
// Before this, an AWS describe-instance-attribute call that hung
// (idle-stopped instance, expired SSO token) kept the button
// `disabled` forever, so the user saw an icon that did nothing on
// click. The button stays interactive while we don't know the state;
// the user picks a direction and the mutation reports its own
// outcome.
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

// useStopProtectionMutators isolates the Wails-bound RPC and toast
// feedback. Each outcome dispatches a notification: success toasts
// confirm the AWS attribute is now what the user asked for; errors
// surface the raw AWS message verbatim so a missing SSO token or
// instance-profile permission stays actionable.
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

// StopProtectionConfirmDialog asks the user to confirm the lock
// transition (the destructive direction — it also breaks the user's
// own manual Stop until reversed). Unlocking is one-click and does
// not route through this dialog.
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

// StopProtectionToggle renders the lock/unlock icon that flips the AWS
// DisableApiStop attribute for the selected cloud context. Locking is
// the recovery lever the user reaches for when the in-pod idle monitor
// is racing against a repair (e.g. erun-mcp is in ImagePullBackOff so
// every activity marker is idle and the monitor decides to stop the
// instance every 10 min). While locked, every ec2:StopInstances call
// returns OperationNotPermitted — including the user's own desktop
// Stop button — so the lock icon also blocks the adjacent Power
// button's effect until the user clears it.
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

// tooltipLineClassName mirrors the leading-space convention used by
// idleStatusTooltipLines: "- " is a top-level entry (Active markers, etc.),
// "  - " is a nested per-client detail line under its marker. HTML collapses
// the leading spaces in the rendered output, so we encode the nesting as a
// Tailwind padding class instead.
function tooltipLineClassName(line: string): string | undefined {
  if (line.startsWith('  - ')) {
    return 'pl-6';
  }
  if (line.startsWith('- ')) {
    return 'pl-2';
  }
  return undefined;
}
