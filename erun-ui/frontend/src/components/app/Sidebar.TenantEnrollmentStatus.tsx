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
    // The same building glyph the dashboard renders for this state
    // (TenantPlatformState.tsx's TenantMismatchState): one situation, one
    // shape. It shares declined's colour and is told apart by shape, never by
    // colour alone (WCAG 1.4.1) -- and its label carries the whole meaning
    // anyway.
    return <Building2 aria-hidden="true" className="size-2.5 text-amber-500" />;
  }
  return (
    <span
      aria-hidden="true"
      className="block size-2 rounded-full bg-emerald-500 shadow-[0_0_0_1px_color-mix(in_oklch,currentColor_20%,transparent)]"
    />
  );
}

// enrollmentPlatformName is what the copy calls the platform behind this
// status: the host the row's credential actually reached, or the generic
// "the hosted platform" when it reached none. A literal hostname here was
// only ever correct on a machine talking to that one platform, and this app
// is happy to be configured against several -- so it names the one that
// answered, and claims no hostname at all when none did.
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
      // The enrolled claim this state replaces was false in one specific way,
      // so this says the true thing instead of only negating it: the
      // credential authenticated and resolved, to a tenant that is not this
      // one. The Go side never sets this state without a name; the fallback
      // keeps a hand-built status from rendering "the tenant ".
      const resolved = status.platformTenant?.trim();
      return resolved
        ? `${tenant}'s connection to ${platform} belongs to the tenant ${resolved}`
        : `${tenant}'s connection to ${platform} belongs to a different tenant`;
    }
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

// TenantMismatchPopoverBody explains the one state whose remedy is not in this
// popover: the credential is fine, it just resolves to another tenant, and
// changing that means connecting this local tenant to the platform that
// serves it -- a form the dashboard already owns (TenantPlatformState.tsx's
// TenantMismatchState). So this states the situation and hands the operator
// there, rather than offering a second, drifting way to do the same thing.
//
// It is the sidebar's half of a sentence erun-console already says for the
// same verdict: a tenant reachable only by an account belonging elsewhere
// cannot be reached by signing in again. Naming that here is what keeps the
// operator from retrying the one action that cannot possibly work.
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
  const platform = enrollmentPlatformName(status);
  const label = enrollmentGlyphLabel(state, tenantName, status);
  const navigateToDashboard = (): void => {
    setOpen(false);
    dispatch(openTenantDashboard(tenantName));
  };

  // Enrolled has nothing left to act on, and unknown has nothing yet
  // resolved to act on -- both render as a plain button that opens the
  // dashboard (where a genuine failure gets a real retry path) rather than a
  // popover with actions that would not apply.
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

// EnrollmentPopoverBody selects the body for the states that have something to
// say beyond their label: a separate component so the button above stays under
// the module's complexity budget, which is what it was already at the limit of
// before the mismatch state joined the vocabulary.
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
      // Unreachable behind the guard above, which sends enrolled and unknown
      // down the plain-button branch; a popover with no body rather than a
      // body chosen by falling through into a case it does not match.
      return null;
  }
}
