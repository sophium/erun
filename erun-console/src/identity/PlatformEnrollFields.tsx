import { FieldLabel, Input, SelectField } from 'erun-kit';
import * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';
import type { PlatformEnrollState } from './platformEnrollController';

// TenantTargetSelect is shown only to an OPERATIONS caller — the same
// tenant.type check `shell/sections.ts` already gates the Users/Tenants nav
// entries on, reused here rather than a new permission mechanism. A
// non-OPERATIONS caller never reaches a component that renders this (the nav
// gate already keeps them out), but the check stays local too so the control
// itself never depends only on the surrounding panel for that guarantee.
export function TenantTargetSelect({
  tenantType,
  tenants,
  value,
  onChange,
}: {
  tenantType: string;
  tenants: PlatformTenant[];
  value: string;
  onChange: (tenantId: string) => void;
}): React.ReactElement | null {
  if (tenantType !== 'OPERATIONS') {
    return null;
  }
  return (
    <SelectField
      id="enroll-target-tenant"
      label="Tenant"
      value={value}
      options={tenants.map((tenant) => ({ value: tenant.tenantId, label: tenant.name }))}
      onChange={onChange}
    />
  );
}

// FirstUserNotice fires only on an exact, resolved 0 — never on an
// undefined/unresolved userCount — so a tenant whose count hasn't loaded yet
// never gets told it is about to receive its first user.
export function FirstUserNotice({
  tenant,
}: {
  tenant: PlatformTenant | undefined;
}): React.ReactElement | null {
  if (tenant?.userCount !== 0) {
    return null;
  }
  return (
    <p className="text-sm text-muted-foreground" role="status">
      {tenant.name} has no users yet — enrolling here makes this person its first user, and they
      will be granted TenantAdmin.
    </p>
  );
}

export function PlatformEnrollUsernameFields({
  username,
  setUsername,
  issuer,
  setIssuer,
  subject,
  setSubject,
}: {
  username: string;
  setUsername: (value: string) => void;
  issuer: string;
  setIssuer: (value: string) => void;
  subject: string;
  setSubject: (value: string) => void;
}): React.ReactElement {
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="enroll-platform-username" required>
          Username
        </FieldLabel>
        <Input
          id="enroll-platform-username"
          value={username}
          onChange={(e) => {
            setUsername(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="enroll-platform-issuer">Issuer (optional)</FieldLabel>
        <Input
          id="enroll-platform-issuer"
          value={issuer}
          onChange={(e) => {
            setIssuer(e.target.value);
          }}
          placeholder="https://idp.example.com"
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="enroll-platform-subject">Subject (optional)</FieldLabel>
        <Input
          id="enroll-platform-subject"
          value={subject}
          onChange={(e) => {
            setSubject(e.target.value);
          }}
        />
      </div>
    </>
  );
}

function tenantLabel(tenants: PlatformTenant[], tenantId: string): string {
  return tenants.find((tenant) => tenant.tenantId === tenantId)?.name ?? tenantId;
}

// PlatformEnrollFeedback renders alreadyEnrolled and a genuine username
// collision as two distinct messages (erun#1744's acceptance criterion) —
// the backend already discriminates them (200+alreadyEnrolled vs 409
// USERNAME_TAKEN); this is where the console stops collapsing them into one.
export function PlatformEnrollFeedback({
  state,
  tenants,
  becameFirstUser,
}: {
  state: PlatformEnrollState;
  tenants: PlatformTenant[];
  becameFirstUser: boolean;
}): React.ReactElement | null {
  if (state.status === 'enrolled') {
    if (state.alreadyEnrolled) {
      return (
        <p className="text-sm text-muted-foreground" role="status">
          This identity is already enrolled, as {state.user.username} in{' '}
          {tenantLabel(tenants, state.user.tenantId)}.
        </p>
      );
    }
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Enrolled {state.user.username} in {tenantLabel(tenants, state.user.tenantId)}.
        {becameFirstUser
          ? ' They are this tenant’s first user and have been granted TenantAdmin.'
          : ''}
      </p>
    );
  }
  if (state.status === 'conflict') {
    return (
      <p className="text-sm text-destructive" role="alert">
        A different user already holds that username in the target tenant: {state.message}
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not enroll user: {state.message}
      </p>
    );
  }
  return null;
}
