import { Button, EmptyState, FieldLabel, Input, StatusBadge } from 'erun-kit';
import {
  Copy,
  KeyRound,
  Link2,
  LoaderCircle,
  RefreshCw,
  Send,
  ShieldAlert,
  UserPlus,
  Users,
} from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { HOSTED_PLATFORM_API_URL } from '@/app/hostedPlatform';
import { showNotification } from '@/app/notificationThunks';
import { tenantDashboardEnvironmentName } from '@/app/tenantDashboardPanels';
import {
  chooseTenantPlatformAlias,
  loadTenantDashboard,
  refreshTenantDashboard,
} from '@/app/tenantDialogThunks';
import { openRequestInvitationDialog } from '@/app/tenantInviteRequestThunks';
import {
  connectTenantPlatform,
  enrollTenantPlatformUser,
  setConnectApiUrlDraft,
  setEnrollUsernameDraft,
} from '@/app/tenantPlatformConnectThunks';
import {
  INVITE_REQUEST_KIND_CREATE_TENANT,
  INVITE_REQUEST_STATUS_DECLINED,
  INVITE_REQUEST_STATUS_PENDING,
  TENANT_PLATFORM_STATE_CHOOSE_ALIAS,
  TENANT_PLATFORM_STATE_NO_PERMISSION,
  TENANT_PLATFORM_STATE_NOT_CONNECTED,
  TENANT_PLATFORM_STATE_NOT_ENROLLED,
  TENANT_PLATFORM_STATE_NOT_SIGNED_IN,
  type UITenantDashboard,
} from '@/types';

import { ClipboardSetText } from '../../../wailsjs/runtime/runtime';
import { InlineAlert } from './InlineAlert';
import { SignInAction } from './PlatformSignInAlert';
import { RequestInvitationDialog } from './RequestInvitationDialog';

// TenantPlatformState.tsx renders the tenant dashboard's four/five platform-
// readiness states: not-connected, choose-alias, not-signed-in,
// not-enrolled, no-permission. Each is a distinct user situation with its
// own next action — never one generic "sign in again" sentence — per the
// repo's "Smooth, Seamless, No Dead Ends" standard: a state with no action
// is a defect of the same severity as a crash.

export function TenantPlatformStateCard({
  data,
}: {
  data: UITenantDashboard;
}): React.ReactElement | null {
  switch (data.platformState) {
    case TENANT_PLATFORM_STATE_NOT_CONNECTED:
      return <NotConnectedState />;
    case TENANT_PLATFORM_STATE_CHOOSE_ALIAS:
      return <ChooseAliasState choices={data.platformAliasChoices ?? []} />;
    case TENANT_PLATFORM_STATE_NOT_SIGNED_IN:
      return <NotSignedInState data={data} />;
    case TENANT_PLATFORM_STATE_NOT_ENROLLED:
      return <NotEnrolledState data={data} />;
    case TENANT_PLATFORM_STATE_NO_PERMISSION:
      return <NoPermissionState data={data} />;
    default:
      return null;
  }
}

// PlatformContactLine names what the surface actually talked to: a resolved
// URL and identity, so a misconfiguration is self-diagnosing rather than
// requiring a network trace.
function PlatformContactLine({ data }: { data: UITenantDashboard }): React.ReactElement | null {
  if (!data.platformUrl && !data.platformAlias) {
    return null;
  }
  return (
    <div className="grid gap-0.5 text-[12px] text-muted-foreground">
      {data.platformUrl && (
        <div>
          Platform: <span className="font-mono">{data.platformUrl}</span>
        </div>
      )}
      {data.platformAlias && (
        <div>
          Identity: <span className="font-mono">{data.platformAlias}</span>
        </div>
      )}
    </div>
  );
}

function NotConnectedState(): React.ReactElement {
  const dispatch = useAppDispatch();
  const draft = useAppSelector((state) => state.tenantDashboard.connectApiUrlDraft);
  const connecting = useAppSelector((state) => state.tenantDashboard.connecting);
  const error = useAppSelector((state) => state.tenantDashboard.connectError);
  // Prefill with the one hosted platform's own API URL — a property of the
  // platform, not of the tenant connecting to it. Interpolating the tenant
  // name here previously produced a host that only ever resolved for
  // whichever tenant happened to share a name with the platform's own
  // namespace, and NXDOMAIN for every other tenant; do not reintroduce that
  // shape. Still fully editable — this is a starting guess the Connect
  // action itself verifies against the real platform, not an assumption the
  // app is asking the operator to trust blindly.
  React.useEffect(() => {
    if (!draft.trim()) {
      dispatch(setConnectApiUrlDraft(HOSTED_PLATFORM_API_URL));
    }
  }, [dispatch, draft]);
  return (
    <EmptyState
      icon={<Link2 />}
      heading="Connect this tenant to erunpaas.com"
      body="This tenant isn't connected to a hosted erun platform yet, so Reviews, Merge queue, Builds, Users, and the Audit log can't load."
      action={
        <div className="grid w-full max-w-sm gap-2 text-left">
          <FieldLabel htmlFor="connect-platform-url" required>
            Platform API URL
          </FieldLabel>
          <Input
            id="connect-platform-url"
            placeholder={HOSTED_PLATFORM_API_URL}
            value={draft}
            disabled={connecting}
            onChange={(event) => {
              dispatch(setConnectApiUrlDraft(event.target.value));
            }}
          />
          <Button
            type="button"
            disabled={connecting || !draft.trim()}
            onClick={() => {
              void dispatch(connectTenantPlatform(draft || HOSTED_PLATFORM_API_URL));
            }}
          >
            {connecting && <LoaderCircle className="animate-spin" aria-hidden="true" />}
            {connecting ? 'Connecting…' : 'Connect'}
          </Button>
          {error && <InlineAlert>{error}</InlineAlert>}
        </div>
      }
    />
  );
}

function ChooseAliasState({ choices }: { choices: string[] }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <EmptyState
      icon={<Users />}
      heading="Choose which platform to use"
      body="This machine has more than one erun platform connection configured. Pick the one this tenant reads from."
      action={
        <div className="grid w-full max-w-sm gap-2">
          {choices.map((alias) => (
            <Button
              key={alias}
              type="button"
              variant="outline"
              onClick={() => {
                void dispatch(chooseTenantPlatformAlias(alias));
              }}
            >
              {alias}
            </Button>
          ))}
        </div>
      }
    />
  );
}

function NotSignedInState({ data }: { data: UITenantDashboard }): React.ReactElement {
  const dispatch = useAppDispatch();
  const alias = data.platformAlias ?? '';
  return (
    <EmptyState
      icon={<KeyRound />}
      heading="Sign in to the erun platform"
      body={
        <div className="grid gap-2">
          <p>Your session for this platform has expired or was never started.</p>
          <PlatformContactLine data={data} />
        </div>
      }
      action={
        <SignInAction
          alias={alias}
          onRecovered={() => {
            void dispatch(loadTenantDashboard());
          }}
        />
      }
    />
  );
}

function NotEnrolledState({ data }: { data: UITenantDashboard }): React.ReactElement {
  const dispatch = useAppDispatch();
  const usernameDraft = useAppSelector((state) => state.tenantDashboard.enrollUsernameDraft);
  const enrolling = useAppSelector((state) => state.tenantDashboard.enrolling);
  const enrollError = useAppSelector((state) => state.tenantDashboard.enrollError);
  const tenant = useAppSelector((state) =>
    state.tenants.tenants.find((candidate) => candidate.name === data.tenant),
  );
  const localEnvironmentName = tenantDashboardEnvironmentName(tenant, data.environment);
  const issuer = data.platformIssuer ?? '';
  const subject = data.platformSubject ?? '';
  const command = `erun platform user enroll --username ${usernameDraft.trim() || '<username>'} --issuer ${issuer} --subject ${subject}`;
  return (
    <EmptyState
      icon={<UserPlus />}
      heading="This identity isn't enrolled in this tenant yet"
      body={
        <div className="grid gap-2 text-left">
          <p>
            Signing in again will not help — the platform does not recognize this identity for this
            tenant. An administrator can enroll it, you can try if you already hold user-management
            access, or you can ask an operator for an invitation below.
          </p>
          <PlatformContactLine data={data} />
        </div>
      }
      action={
        <div className="grid w-full max-w-xl gap-3 text-left">
          <div className="grid gap-2">
            <FieldLabel htmlFor="enroll-username" required>
              Username
            </FieldLabel>
            <Input
              id="enroll-username"
              placeholder="jane"
              value={usernameDraft}
              disabled={enrolling}
              onChange={(event) => {
                dispatch(setEnrollUsernameDraft(event.target.value));
              }}
            />
            <Button
              type="button"
              variant="outline"
              disabled={enrolling || !usernameDraft.trim()}
              onClick={() => {
                void dispatch(enrollTenantPlatformUser());
              }}
            >
              {enrolling && <LoaderCircle className="animate-spin" aria-hidden="true" />}
              {enrolling ? 'Enrolling…' : 'Try to enroll myself'}
            </Button>
            {enrollError && <InlineAlert>{enrollError}</InlineAlert>}
          </div>
          <RequestInvitationAction data={data} />
          <div className="grid gap-2 rounded-[var(--radius)] border border-border bg-muted/30 p-3 text-left">
            <div className="flex items-baseline justify-between gap-2">
              <FieldLabel htmlFor="enroll-admin-command">Or ask an administrator to run</FieldLabel>
              <CopyCommandButton command={command} />
            </div>
            {/* whitespace-pre-wrap + break-all (not overflow-x-auto) so the
                full issuer/subject values are always visible on their own,
                never clipped at the card edge with no visual cue that more
                text exists — the whole point of showing this command is for
                the administrator to read and verify it, not just copy it
                blind. */}
            <code
              id="enroll-admin-command"
              className="block overflow-x-hidden whitespace-pre-wrap break-words rounded bg-background px-2 py-1.5 text-[12px]"
            >
              {command}
            </code>
          </div>
          <RequestInvitationDialog data={data} localEnvironmentName={localEnvironmentName} />
        </div>
      }
    />
  );
}

// RequestInvitationAction is NotEnrolledState's second option: its own
// rendering distinguishes none-yet (offer the action) from
// pending/declined (show status — no cancel/withdraw exists server-side, so
// none is offered) — never silently reusing "Try to enroll myself"'s button
// for a fundamentally different request.
function RequestInvitationAction({ data }: { data: UITenantDashboard }): React.ReactElement {
  const dispatch = useAppDispatch();
  const request = data.myInviteRequest;
  if (!request) {
    // A failed read is never presented as "never requested" -- the caller
    // could already have a pending or approved request this load just
    // couldn't see, and offering the submit action would let them resubmit
    // into whatever the platform actually holds.
    if (data.myInviteRequestError) {
      return (
        <div className="grid gap-2 rounded-[var(--radius)] border border-border bg-muted/30 p-3">
          <InlineAlert>
            Your invitation request status could not be checked: {data.myInviteRequestError}
          </InlineAlert>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="justify-self-start"
            onClick={() => {
              void dispatch(refreshTenantDashboard());
            }}
          >
            <RefreshCw aria-hidden="true" />
            Try again
          </Button>
        </div>
      );
    }
    return (
      <Button
        type="button"
        variant="outline"
        onClick={() => {
          dispatch(openRequestInvitationDialog());
        }}
      >
        <Send aria-hidden="true" />
        Request an invitation
      </Button>
    );
  }
  if (request.status === INVITE_REQUEST_STATUS_PENDING) {
    return (
      <div className="grid gap-2 rounded-[var(--radius)] border border-border bg-muted/30 p-3">
        <div className="flex items-center gap-2">
          <StatusBadge tone="in-progress" label="Pending" />
          <span className="text-[13px] text-muted-foreground">
            Your request to{' '}
            {request.kind === INVITE_REQUEST_KIND_CREATE_TENANT ? 'register' : 'join'}{' '}
            {request.tenantName} is waiting on an operator.
          </span>
        </div>
      </div>
    );
  }
  if (request.status === INVITE_REQUEST_STATUS_DECLINED) {
    return (
      <div className="grid gap-2 rounded-[var(--radius)] border border-border bg-muted/30 p-3">
        <div className="flex items-center gap-2">
          <StatusBadge tone="destructive" label="Declined" />
        </div>
        <p className="text-[13px] text-muted-foreground">
          {request.declineReason ?? 'No reason was given.'}
        </p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="justify-self-start"
          onClick={() => {
            dispatch(openRequestInvitationDialog());
          }}
        >
          <Send aria-hidden="true" />
          Request again
        </Button>
      </div>
    );
  }
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-border bg-muted/30 p-3">
      <div className="flex items-center gap-2">
        <StatusBadge tone="success" label="Approved" />
        <span className="text-[13px] text-muted-foreground">
          Refresh the dashboard to finish signing in.
        </span>
      </div>
    </div>
  );
}

// CopyCommandButton is the one-click copy the not-enrolled hand-off needs:
// the operator's explicit ask is that the administrator never has to
// reconstruct the enrollment command by hand.
function CopyCommandButton({ command }: { command: string }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      aria-label="Copy enrollment command"
      onClick={() => {
        void (async () => {
          await ClipboardSetText(command);
          dispatch(showNotification('success', 'Copied the enrollment command.'));
        })();
      }}
    >
      <Copy aria-hidden="true" />
    </Button>
  );
}

function NoPermissionState({ data }: { data: UITenantDashboard }): React.ReactElement {
  return (
    <EmptyState
      icon={<ShieldAlert />}
      heading="You do not have access to this tenant's dashboard"
      body={
        <div className="grid gap-2 text-left">
          <p>Ask a tenant administrator to grant you access to the platform.</p>
          <PlatformContactLine data={data} />
        </div>
      }
    />
  );
}
