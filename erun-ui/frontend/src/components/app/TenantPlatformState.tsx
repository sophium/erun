import { Button, EmptyState, FieldLabel, Input } from 'erun-kit';
import { KeyRound, Link2, LoaderCircle, ShieldAlert, UserPlus, Users } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { chooseTenantPlatformAlias, loadTenantDashboard } from '@/app/tenantDialogThunks';
import {
  connectTenantPlatform,
  enrollTenantPlatformUser,
  setConnectApiUrlDraft,
  setEnrollUsernameDraft,
} from '@/app/tenantPlatformConnectThunks';
import {
  TENANT_PLATFORM_STATE_CHOOSE_ALIAS,
  TENANT_PLATFORM_STATE_NO_PERMISSION,
  TENANT_PLATFORM_STATE_NOT_CONNECTED,
  TENANT_PLATFORM_STATE_NOT_ENROLLED,
  TENANT_PLATFORM_STATE_NOT_SIGNED_IN,
  type UITenantDashboard,
} from '@/types';

import { InlineAlert } from './InlineAlert';
import { SignInAction } from './PlatformSignInAlert';

// TenantPlatformState.tsx renders the tenant dashboard's four/five platform-
// readiness states (#1393): not-connected, choose-alias, not-signed-in,
// not-enrolled, no-permission. Each is a distinct user situation with its
// own next action — never one generic "sign in again" sentence — per the
// repo's "Smooth, Seamless, No Dead Ends" standard: a state with no action
// is a defect of the same severity as a crash.

export function TenantPlatformStateCard({
  tenant,
  data,
}: {
  tenant: string;
  data: UITenantDashboard;
}): React.ReactElement | null {
  switch (data.platformState) {
    case TENANT_PLATFORM_STATE_NOT_CONNECTED:
      return <NotConnectedState tenant={tenant} />;
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

// PlatformContactLine names what the surface actually talked to (#1393,
// defect 4 in the operator's screenshot): a resolved URL and identity, so a
// misconfiguration is self-diagnosing rather than requiring a network trace.
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

function NotConnectedState({ tenant }: { tenant: string }): React.ReactElement {
  const dispatch = useAppDispatch();
  const draft = useAppSelector((state) => state.tenantDashboard.connectApiUrlDraft);
  const connecting = useAppSelector((state) => state.tenantDashboard.connecting);
  const error = useAppSelector((state) => state.tenantDashboard.connectError);
  const placeholder = `https://api.${tenant}-prod.services.erunpaas.com`;
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
            placeholder={placeholder}
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
              void dispatch(connectTenantPlatform(draft || placeholder));
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
            tenant. An administrator can enroll it, or you can try if you already hold
            user-management access.
          </p>
          <PlatformContactLine data={data} />
        </div>
      }
      action={
        <div className="grid w-full max-w-md gap-3 text-left">
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
          <div className="grid gap-1 rounded-[var(--radius)] border border-border bg-muted/30 p-3 text-left">
            <FieldLabel htmlFor="enroll-admin-command">Or ask an administrator to run</FieldLabel>
            <code
              id="enroll-admin-command"
              className="block overflow-x-auto whitespace-pre rounded bg-background px-2 py-1.5 text-[12px]"
            >
              {command}
            </code>
          </div>
        </div>
      }
    />
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
