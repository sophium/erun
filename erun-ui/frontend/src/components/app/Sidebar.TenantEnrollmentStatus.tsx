import { Button, IconTooltip, Popover, PopoverContent, PopoverTrigger } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { openTenantDashboard } from '@/app/tenantDialogThunks';
import { useTenantEnrollmentStatus } from '@/app/tenantEnrollmentPoll';
import {
  TENANT_ENROLLMENT_DECLINED,
  TENANT_ENROLLMENT_ENROLLED,
  TENANT_ENROLLMENT_LOCAL_ONLY,
  TENANT_ENROLLMENT_PENDING,
} from '@/types';

// Sidebar.TenantEnrollmentStatus.tsx is the tenant row's platform-enrollment
// status icon: the whole request/approve flow reduces to one status icon on
// the tenant row, carrying both the state and the control at every stage. It
// is a THIRD row-kind status glyph, deliberately not built on
// Sidebar.StatusDot.tsx's StatusDotGlyph -- that component's own doc comment
// scopes it to the env/orchestrator "condition" vocabulary (running / busy /
// stopped / failed), and repurposing its exported union for an unrelated
// domain (platform enrollment) would make a future env-only change silently
// affect this glyph too. Shape still carries the state, not colour alone
// (WCAG 1.4.1), and the pending glyph deliberately mirrors StatusDotGlyph's
// busy treatment pixel-for-pixel: "a request is being worked on" is the same
// concept as "a command is running in there".

type TenantEnrollmentGlyphState = 'local-only' | 'pending' | 'declined' | 'enrolled';

function EnrollmentGlyph({ state }: { state: TenantEnrollmentGlyphState }): React.ReactElement {
  if (state === 'local-only') {
    return (
      <span
        aria-hidden="true"
        className="block size-2 rounded-full border-[1.5px] border-muted-foreground bg-transparent"
      />
    );
  }
  if (state === 'pending') {
    return (
      <span
        aria-hidden="true"
        className="flex size-3 items-center justify-center rounded-full border-[1.5px] border-emerald-500 motion-safe:animate-pulse"
      >
        <span className="block size-1.5 rounded-full bg-emerald-500" />
      </span>
    );
  }
  if (state === 'declined') {
    return <TriangleAlert aria-hidden="true" className="size-2.5 text-amber-500" />;
  }
  return (
    <span
      aria-hidden="true"
      className="block size-2 rounded-full bg-emerald-500 shadow-[0_0_0_1px_color-mix(in_oklch,currentColor_20%,transparent)]"
    />
  );
}

function enrollmentGlyphLabel(state: TenantEnrollmentGlyphState, tenant: string): string {
  switch (state) {
    case 'local-only':
      return `${tenant} is not on erunpaas.com yet`;
    case 'pending':
      return `${tenant}'s invitation request is pending`;
    case 'declined':
      return `${tenant}'s invitation request was declined`;
    case 'enrolled':
      return `${tenant} is enrolled in erunpaas.com`;
  }
}

// LocalOnlyPopoverBody: clicking this state is described as "opens the
// dialog" for requesting an invitation or signing in with an existing
// account. The request FORM itself belongs in the tenant dashboard's
// NotEnrolledState, which is a parallel, in-flight piece of work this file
// must not duplicate -- so both actions here navigate to that dashboard
// rather than re-implementing the request or sign-in flow inline in the
// sidebar.
function LocalOnlyPopoverBody({
  tenant,
  onNavigate,
}: {
  tenant: string;
  onNavigate: () => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2 text-left text-sm">
      <p className="font-medium">Not on erunpaas.com yet</p>
      <p className="text-xs text-muted-foreground">
        Ask to join or register {tenant} on the hosted platform, or sign in if you already have
        access.
      </p>
      <div className="grid gap-1.5">
        <Button type="button" size="sm" onClick={onNavigate}>
          Request an invitation
        </Button>
        <Button type="button" size="sm" variant="outline" onClick={onNavigate}>
          Sign in
        </Button>
      </div>
    </div>
  );
}

function PendingPopoverBody({ tenant }: { tenant: string }): React.ReactElement {
  return (
    <div className="grid gap-1 text-left text-sm" role="status">
      <p className="font-medium">Request pending</p>
      <p className="text-xs text-muted-foreground">
        Your request to join or register {tenant} is waiting on an operator. Nothing else to do
        while you wait -- there is no way to withdraw a request yet.
      </p>
    </div>
  );
}

function DeclinedPopoverBody({
  tenant,
  declineReason,
  onNavigate,
}: {
  tenant: string;
  declineReason: string;
  onNavigate: () => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2 text-left text-sm">
      <p className="font-medium">Request declined</p>
      <p className="text-xs text-muted-foreground">
        Your request to join or register {tenant} was declined
        {declineReason ? `: ${declineReason}` : '.'}
      </p>
      <Button type="button" size="sm" variant="outline" onClick={onNavigate}>
        Try again
      </Button>
    </div>
  );
}

// TenantEnrollmentStatusButton is the row's own hit target, a sibling of
// TenantSelectButton/TenantManageButton in Sidebar.TenantGroup.tsx -- never
// hover-gated, since a status indicator must stay visible (mirrors
// Sidebar.EnvironmentRow.tsx's EnvStatusIndicator). It never intercepts the
// row's own click (toggleTenantCollapsed/openTenantDashboard): every branch
// below stops propagation before doing anything.
export function TenantEnrollmentStatusButton({
  tenantName,
}: {
  tenantName: string;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const [open, setOpen] = React.useState(false);
  const status = useTenantEnrollmentStatus(tenantName);
  if (!status) {
    return null;
  }
  const state = status.state as TenantEnrollmentGlyphState;
  const label = enrollmentGlyphLabel(state, tenantName);
  const navigateToDashboard = (): void => {
    setOpen(false);
    dispatch(openTenantDashboard(tenantName));
  };

  if (state === TENANT_ENROLLMENT_ENROLLED) {
    return (
      <IconTooltip label={label}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-[18px] flex-none cursor-pointer rounded-full border-0 bg-transparent p-0 text-current hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)]"
          aria-label={label}
          data-testid="tenant-enrollment-status"
          data-enrollment-state={state}
          onClick={(event) => {
            event.stopPropagation();
            dispatch(openTenantDashboard(tenantName));
          }}
        >
          <EnrollmentGlyph state={state} />
        </Button>
      </IconTooltip>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <IconTooltip label={label}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-[18px] flex-none cursor-pointer rounded-full border-0 bg-transparent p-0 text-current hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)]"
            aria-label={label}
            data-testid="tenant-enrollment-status"
            data-enrollment-state={state}
            onClick={(event) => {
              event.stopPropagation();
            }}
          >
            <EnrollmentGlyph state={state} />
          </Button>
        </PopoverTrigger>
      </IconTooltip>
      <PopoverContent side="right" align="start" className="w-72 space-y-1 p-3">
        {state === TENANT_ENROLLMENT_LOCAL_ONLY && (
          <LocalOnlyPopoverBody tenant={tenantName} onNavigate={navigateToDashboard} />
        )}
        {state === TENANT_ENROLLMENT_PENDING && <PendingPopoverBody tenant={tenantName} />}
        {state === TENANT_ENROLLMENT_DECLINED && (
          <DeclinedPopoverBody
            tenant={tenantName}
            declineReason={status.declineReason ?? ''}
            onNavigate={navigateToDashboard}
          />
        )}
      </PopoverContent>
    </Popover>
  );
}
