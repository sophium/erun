import { Button } from 'erun-kit';
import * as React from 'react';

import { useListTenantsQuery } from '../app/api/tenantsApi';
import { TenantTargetSelect } from '../shell/TenantTargetSelect';
import { useTenantTargetSelection } from '../shell/useTenantTargetSelection';
import { usePlatformEnrollController } from './platformEnrollController';
import {
  FirstUserNotice,
  PlatformEnrollFeedback,
  PlatformEnrollUsernameFields,
} from './PlatformEnrollFields';

// EnrollUserForm is the console's parity for `erun platform user enroll`
// (erun#1744): it writes a user row directly via POST /v1/users, optionally
// targeting a tenant other than the caller's own. The tenant selector only
// ever renders for an OPERATIONS caller (TenantTargetSelect); every other
// caller enrolls into their own tenant with no selector shown at all.
export function EnrollUserForm({
  token,
  ownTenantId,
  tenantType,
}: {
  token: string;
  ownTenantId: string;
  tenantType: string;
}): React.ReactElement {
  const tenantsQuery = useListTenantsQuery(token);
  const tenants = React.useMemo(() => tenantsQuery.data ?? [], [tenantsQuery.data]);
  const { targetTenantId, setTargetTenantId } = useTenantTargetSelection(ownTenantId);
  const [username, setUsername] = React.useState('');
  const [issuer, setIssuer] = React.useState('');
  const [subject, setSubject] = React.useState('');
  const [becameFirstUser, setBecameFirstUser] = React.useState(false);
  const { state, enroll, reset } = usePlatformEnrollController(token);

  const targetTenant = tenants.find((tenant) => tenant.tenantId === targetTenantId);
  const busy = state.status === 'enrolling';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    reset();
    setBecameFirstUser(targetTenant?.userCount === 0);
    enroll({
      username: username.trim(),
      issuer: issuer.trim() || undefined,
      subject: subject.trim() || undefined,
      tenantId: targetTenantId !== ownTenantId ? targetTenantId : undefined,
    });
  };

  return (
    <form
      className="grid max-w-md gap-3"
      onSubmit={submit}
      aria-labelledby="enroll-platform-user-heading"
    >
      <h3 id="enroll-platform-user-heading" className="text-sm font-semibold text-foreground">
        Enroll a user directly
      </h3>
      <p className="text-xs text-muted-foreground">
        Writes a user row immediately, linking the (issuer, subject) identity if given. Leave both
        blank to enroll a username that cannot sign in until an identity is linked later.
      </p>
      <TenantTargetSelect
        id="enroll-user-target-tenant"
        tenantType={tenantType}
        tenants={tenants}
        value={targetTenantId}
        onChange={setTargetTenantId}
      />
      <PlatformEnrollUsernameFields
        username={username}
        setUsername={setUsername}
        issuer={issuer}
        setIssuer={setIssuer}
        subject={subject}
        setSubject={setSubject}
      />
      <FirstUserNotice tenant={targetTenant} />
      <Button type="submit" disabled={busy} className="justify-self-start">
        {busy ? 'Enrolling…' : 'Enroll directly'}
      </Button>
      <PlatformEnrollFeedback state={state} tenants={tenants} becameFirstUser={becameFirstUser} />
    </form>
  );
}
