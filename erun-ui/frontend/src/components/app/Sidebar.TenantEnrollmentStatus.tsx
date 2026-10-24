import { Button, IconTooltip, Popover, PopoverContent, PopoverTrigger } from 'erun-kit';
import { Building2, CircleHelp, TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { openTenantDashboard } from '@/app/tenantDialogThunks';
import { useTenantEnrollmentStatus } from '@/app/tenantEnrollmentPoll';
import {
  TENANT_ENROLLMENT_DECLINED,
  TENANT_ENROLLMENT_ENROLLED,
  TENANT_ENROLLMENT_LOCAL_ONLY,
  TENANT_ENROLLMENT_PENDING,
  TENANT_ENROLLMENT_TENANT_MISMATCH,
  TENANT_ENROLLMENT_UNKNOWN,
  type UITenantPlatformEnrollmentStatus,
} from '@/types';

// Tenant row's platform-enrollment status icon. Deliberately not StatusDotGlyph,
// whose union is scoped to env/orchestrator conditions.

type TenantEnrollmentGlyphState =
  | 'local-only'
  | 'pending'
  | 'declined'
  | 'enrolled'
  | 'unknown'
  | 'tenant-mismatch';

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
  if (state === 'unknown') {
    return <CircleHelp aria-hidden="true" className="size-2.5 text-muted-foreground" />;
  }
  if (state === 'tenant-mismatch') {
    // Same glyph as the dashboard's TenantMismatchState: one situation, one shape.
    return <Building2 aria-hidden="true" className="size-2.5 text-amber-500" />;
  }
  return (
    <span
      aria-hidden="true"
      className="block size-2 rounded-full bg-emerald-500 shadow-[0_0_0_1px_color-mix(in_oklch,currentColor_20%,transparent)]"
    />
  );
}

// Names the host the credential actually reached; this app may be configured
// against several platforms.
function enrollmentPlatformName(status: UITenantPlatformEnrollmentStatus): string {
  const host = status.platformHost?.trim() ?? '';
  return host === '' ? 'the hosted platform' : host;
}

function enrollmentGlyphLabel(
  state: TenantEnrollmentGlyphState,
  tenant: string,
  status: UITenantPlatformEnrollmentStatus,
): string {
  const platform = enrollmentPlatformName(status);
  switch (state) {
    case 'local-only':
      return `${tenant} is not on ${platform} yet`;
    case 'pending':
      return `${tenant}'s invitation request is pending`;
    case 'declined':
      return `${tenant}'s invitation request was declined`;
    case 'enrolled':
      return `${tenant} is enrolled in ${platform}`;
    case 'unknown':
      return `${tenant}'s platform enrollment status could not be checked`;
    case 'tenant-mismatch': {
      // The Go side always sets a name; the fallback guards hand-built statuses.
      const resolved = status.platformTenant?.trim();
      return resolved
        ? `${tenant}'s connection to ${platform} belongs to the tenant ${resolved}`
        : `${tenant}'s connection to ${platform} belongs to a different tenant`;
    }
  }
}

// The request form belongs to the dashboard's NotEnrolledState; both actions
// navigate there rather than duplicating it.
function LocalOnlyPopoverBody({
  tenant,
  platform,
  onNavigate,
}: {
  tenant: string;
  platform: string;
  onNavigate: () => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2 text-left text-sm">
      <p className="font-medium">Not on {platform} yet</p>
      <p className="text-xs text-muted-foreground">
        Ask to join or register {tenant} on {platform}, or sign in if you already have access.
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

// The remedy lives in the dashboard's TenantMismatchState; signing in again
// cannot fix a tenant reachable only by an account belonging elsewhere.
function TenantMismatchPopoverBody({
  tenant,
  platform,
  platformTenant,
  onNavigate,
}: {
  tenant: string;
  platform: string;
  platformTenant: string;
  onNavigate: () => void;
}): React.ReactElement {
  const resolved = platformTenant.trim();
  return (
    <div className="grid gap-2 text-left text-sm" role="status">
      <p className="font-medium">Different tenant on {platform}</p>
      <p className="text-xs text-muted-foreground">
        This row is the local tenant {tenant}, but its {platform} connection belongs to{' '}
        {resolved ? `the platform tenant ${resolved}` : 'another platform tenant'}. Any enrolment
        behind this connection is {resolved ? `${resolved}'s` : "that tenant's"}, not {tenant}
        &apos;s, and signing in again cannot change which tenant the credential resolves to.
      </p>
      <Button type="button" size="sm" variant="outline" onClick={onNavigate}>
        Open the dashboard
      </Button>
    </div>
  );
}

// Every branch stops propagation so the row's own click is never intercepted.
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
  const platform = enrollmentPlatformName(status);
  const label = enrollmentGlyphLabel(state, tenantName, status);
  const navigateToDashboard = (): void => {
    setOpen(false);
    dispatch(openTenantDashboard(tenantName));
  };

  if (state === TENANT_ENROLLMENT_ENROLLED || state === TENANT_ENROLLMENT_UNKNOWN) {
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
        <EnrollmentPopoverBody
          state={state}
          status={status}
          tenant={tenantName}
          platform={platform}
          onNavigate={navigateToDashboard}
        />
      </PopoverContent>
    </Popover>
  );
}

function EnrollmentPopoverBody({
  state,
  status,
  tenant,
  platform,
  onNavigate,
}: {
  state: TenantEnrollmentGlyphState;
  status: UITenantPlatformEnrollmentStatus;
  tenant: string;
  platform: string;
  onNavigate: () => void;
}): React.ReactElement | null {
  switch (state) {
    case TENANT_ENROLLMENT_LOCAL_ONLY:
      return <LocalOnlyPopoverBody tenant={tenant} platform={platform} onNavigate={onNavigate} />;
    case TENANT_ENROLLMENT_PENDING:
      return <PendingPopoverBody tenant={tenant} />;
    case TENANT_ENROLLMENT_DECLINED:
      return (
        <DeclinedPopoverBody
          tenant={tenant}
          declineReason={status.declineReason ?? ''}
          onNavigate={onNavigate}
        />
      );
    case TENANT_ENROLLMENT_TENANT_MISMATCH:
      return (
        <TenantMismatchPopoverBody
          tenant={tenant}
          platform={platform}
          platformTenant={status.platformTenant?.trim() ?? ''}
          onNavigate={onNavigate}
        />
      );
    default:
      return null;
  }
}
