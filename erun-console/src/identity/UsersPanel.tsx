import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  Input,
  StatusBadge,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from 'erun-kit';
import { LoaderCircle, Users } from 'lucide-react';
import * as React from 'react';

import type { EnrollIdentityUserInput, IdentityUser } from '../app/api/identityApi';
import { TenantTargetSelect } from '../shell/TenantTargetSelect';
import { useTenantTargetSelection } from '../shell/useTenantTargetSelection';
import type { EnrollState, UsersState } from './controller';
import { resolveUsersScope, useUsersController } from './controller';
import { useEnrollOrgTarget } from './enrollOrgTargetController';
import { EnrollUserForm } from './EnrollUserForm';
import { EnrollBasicFields, OrgTargetStatus } from './PlatformEnrollFields';
import { UserRolesDialog } from './UserRolesDialog';
import { ScopedUsersStatus, UsersScopeBadge } from './usersScope';

// Without mail configured, the temporary password is the only way the person signs in.
function EnrollFeedback({ enroll }: { enroll: EnrollState }): React.ReactElement | null {
  if (enroll.status === 'enrolled') {
    if (enroll.result.error !== undefined) {
      return (
        <p className="text-sm text-destructive" role="alert">
          {enroll.result.idpUser.username} was created in the identity provider (id{' '}
          {enroll.result.idpUser.id}), but could not be enrolled as an erun user:{' '}
          {enroll.result.error}
        </p>
      );
    }
    if (enroll.result.mailDeliveryConfigured) {
      return (
        <p className="text-sm text-muted-foreground" role="status">
          Enrolled {enroll.result.idpUser.username}. An invite email is on its way to complete
          sign-in.
        </p>
      );
    }
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Enrolled {enroll.result.idpUser.username}. Outbound mail is not configured, so no invite
        email was sent — hand them the temporary password shown below.
      </p>
    );
  }
  if (enroll.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not enroll user: {enroll.message}
      </p>
    );
  }
  return null;
}

// Dismissing strips the password from controller state.
function TemporaryPasswordDialog({
  username,
  password,
  onDismiss,
}: {
  username: string;
  password: string;
  onDismiss: () => void;
}): React.ReactElement {
  const [copied, setCopied] = React.useState(false);

  const copy = (): void => {
    void navigator.clipboard.writeText(password).then(() => {
      setCopied(true);
    });
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) {
          onDismiss();
        }
      }}
    >
      <DialogContent aria-labelledby="temp-password-heading">
        <DialogHeader>
          <DialogTitle id="temp-password-heading">Temporary password for {username}</DialogTitle>
          <DialogDescription>
            This is shown once and will not be shown again. Share it with {username} directly; they
            must sign in and change it.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <Input
            readOnly
            value={password}
            aria-label="Temporary password"
            className="font-mono"
            onFocus={(e) => {
              e.target.select();
            }}
          />
          <Button type="button" variant="outline" onClick={copy}>
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </div>
        <DialogFooter>
          <Button type="button" onClick={onDismiss}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EnrollForm({
  token,
  ownTenantId,
  tenantType,
  enroll,
  onEnroll,
}: {
  token: string;
  ownTenantId: string;
  tenantType: string;
  enroll: EnrollState;
  onEnroll: (input: EnrollIdentityUserInput) => void;
}): React.ReactElement {
  const [username, setUsername] = React.useState('');
  const [email, setEmail] = React.useState('');
  const [firstName, setFirstName] = React.useState('');
  const [lastName, setLastName] = React.useState('');
  const busy = enroll.status === 'enrolling';

  const { targetTenantId, setTargetTenantId } = useTenantTargetSelection(ownTenantId);
  const { tenants, orgTarget } = useEnrollOrgTarget(token, ownTenantId, targetTenantId);
  const canSubmit = orgTarget.status === 'default' || orgTarget.status === 'resolved';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    if (orgTarget.status !== 'default' && orgTarget.status !== 'resolved') {
      return;
    }
    onEnroll({
      username: username.trim(),
      email: email.trim(),
      firstName: firstName.trim() || undefined,
      lastName: lastName.trim() || undefined,
      orgId: orgTarget.status === 'resolved' ? orgTarget.orgId : undefined,
    });
  };

  return (
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="enroll-form-heading">
      <h3 id="enroll-form-heading" className="text-sm font-semibold text-foreground">
        Enroll a user
      </h3>
      <EnrollBasicFields
        username={username}
        setUsername={setUsername}
        email={email}
        setEmail={setEmail}
        firstName={firstName}
        setFirstName={setFirstName}
        lastName={lastName}
        setLastName={setLastName}
      />
      <TenantTargetSelect
        id="enroll-target-tenant"
        tenantType={tenantType}
        tenants={tenants}
        value={targetTenantId}
        onChange={setTargetTenantId}
      />
      <OrgTargetStatus target={orgTarget} tenants={tenants} tenantId={targetTenantId} />
      <Button type="submit" disabled={busy || !canSubmit} className="justify-self-start">
        {busy ? 'Enrolling…' : 'Enroll user'}
      </Button>
      <EnrollFeedback enroll={enroll} />
    </form>
  );
}

// Deactivation is confirmed: it blocks the next sign-in and a misclick is invisible.
// Reactivation stays one click.
function DeactivateUserDialog({
  user,
  onCancel,
  onConfirm,
}: {
  user: IdentityUser;
  onCancel: () => void;
  onConfirm: (externalId: string, active: boolean) => Promise<void>;
}): React.ReactElement {
  const [busy, setBusy] = React.useState(false);

  const confirm = (): void => {
    setBusy(true);
    void onConfirm(user.id, false).then(onCancel);
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) {
          onCancel();
        }
      }}
    >
      <DialogContent aria-labelledby="deactivate-user-heading">
        <DialogHeader>
          <DialogTitle id="deactivate-user-heading">Deactivate {user.username}?</DialogTitle>
          <DialogDescription>
            {user.username} will not be able to sign in again until reactivated. This takes effect
            on their next sign-in.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
          <Button type="button" variant="destructive" disabled={busy} onClick={confirm}>
            {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
            Deactivate
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function MembershipBadge({ user }: { user: IdentityUser }): React.ReactElement {
  if (user.isMachine) {
    return <span className="text-xs text-muted-foreground">Machine account</span>;
  }
  if (user.enrolled) {
    return <span className="text-xs font-medium text-foreground">Tenant member</span>;
  }
  return <span className="text-xs text-muted-foreground">IdP only, not enrolled</span>;
}

// The erun username is primary; the IdP username shows only when it differs. The OIDC
// subject is always shown: it is the value joining the erun and IdP directories.
function UsernameCell({
  user,
  isCaller,
}: {
  user: IdentityUser;
  isCaller: boolean;
}): React.ReactElement {
  const showsBothNames =
    user.enrolled && user.erunUsername !== undefined && user.erunUsername !== user.username;
  const primary = showsBothNames ? user.erunUsername : user.username;
  return (
    <div className="grid gap-0.5">
      <div className="flex items-center gap-2">
        <span className="font-medium text-foreground">{primary}</span>
        {isCaller && <StatusBadge tone="muted" label="You" showIcon={false} />}
      </div>
      {showsBothNames && (
        <span className="text-xs text-muted-foreground">IdP username: {user.username}</span>
      )}
      <span className="font-mono text-[10px] text-muted-foreground">Subject: {user.id}</span>
    </div>
  );
}

function UserRow({
  user,
  callerErunUserId,
  onSetActive,
  onRequestDeactivate,
  onManageRoles,
}: {
  user: IdentityUser;
  callerErunUserId: string | undefined;
  onSetActive: (externalId: string, active: boolean) => Promise<void>;
  onRequestDeactivate: (user: IdentityUser) => void;
  onManageRoles: (user: IdentityUser) => void;
}): React.ReactElement {
  const active = user.state === 'USER_STATE_ACTIVE';
  // Machine accounts sit on the sign-in path, so they get no toggle at all.
  const canToggle = !user.isMachine && (active || user.state === 'USER_STATE_INACTIVE');
  const isCaller =
    callerErunUserId !== undefined && user.enrolled && user.erunUserId === callerErunUserId;
  return (
    <TableRow>
      <TableCell>
        <UsernameCell user={user} isCaller={isCaller} />
      </TableCell>
      <TableCell>{user.email ?? ''}</TableCell>
      <TableCell>{user.state}</TableCell>
      <TableCell>
        <MembershipBadge user={user} />
      </TableCell>
      <TableCell>
        {/* Only an enrolled erun user has a row for roles to attach to. */}
        {user.enrolled && user.erunUserId !== undefined && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              onManageRoles(user);
            }}
          >
            Manage roles
          </Button>
        )}
      </TableCell>
      <TableCell>
        {canToggle && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              if (active) {
                onRequestDeactivate(user);
              } else {
                void onSetActive(user.id, true);
              }
            }}
          >
            {active ? 'Deactivate' : 'Reactivate'}
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}

function UsersTable({
  users,
  callerErunUserId,
  onSetActive,
  onRequestDeactivate,
  onManageRoles,
}: {
  users: IdentityUser[];
  callerErunUserId: string | undefined;
  onSetActive: (externalId: string, active: boolean) => Promise<void>;
  onRequestDeactivate: (user: IdentityUser) => void;
  onManageRoles: (user: IdentityUser) => void;
}): React.ReactElement {
  if (users.length === 0) {
    return <EmptyState icon={<Users />} heading="No users enrolled yet." />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Username</TableHead>
          <TableHead>Email</TableHead>
          <TableHead>State</TableHead>
          <TableHead>Membership</TableHead>
          <TableHead>Roles</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {users.map((user) => (
          <UserRow
            key={user.id}
            user={user}
            callerErunUserId={callerErunUserId}
            onSetActive={onSetActive}
            onRequestDeactivate={onRequestDeactivate}
            onManageRoles={onManageRoles}
          />
        ))}
      </TableBody>
    </Table>
  );
}

function UsersBody({
  usersState,
  callerErunUserId,
  onSetActive,
  onRequestDeactivate,
  onManageRoles,
}: {
  usersState: UsersState;
  callerErunUserId: string | undefined;
  onSetActive: (externalId: string, active: boolean) => Promise<void>;
  onRequestDeactivate: (user: IdentityUser) => void;
  onManageRoles: (user: IdentityUser) => void;
}): React.ReactElement {
  if (usersState.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading users…
      </p>
    );
  }
  if (usersState.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load users: {usersState.message}
      </p>
    );
  }
  return (
    <UsersTable
      users={usersState.users}
      callerErunUserId={callerErunUserId}
      onSetActive={onSetActive}
      onRequestDeactivate={onRequestDeactivate}
      onManageRoles={onManageRoles}
    />
  );
}

// Rendered only for an OPERATIONS tenant. EnrollForm's tenant selector targets another
// tenant's Zitadel org, since a user's org decides which tenant they resolve to.
export function UsersPanel({
  token,
  ownTenantId,
  tenantType,
  callerErunUserId,
  scopeTenantId,
}: {
  token: string;
  ownTenantId: string;
  tenantType: string;
  // Undefined until whoami resolves, which renders no "You" badge.
  callerErunUserId?: string;
  // Undefined means the caller's own tenant.
  scopeTenantId?: string;
}): React.ReactElement {
  const targetTenantId = scopeTenantId ?? ownTenantId;
  const { tenants: scopeTenants, orgTarget } = useEnrollOrgTarget(
    token,
    ownTenantId,
    targetTenantId,
  );
  const { scope, ready: scopeReady } = resolveUsersScope(orgTarget, targetTenantId);
  const { usersState, enrollState, enroll, setActive, dismissTemporaryPassword } =
    useUsersController(token, scope, !scopeReady);
  const temporaryPassword =
    enrollState.status === 'enrolled' ? enrollState.result.temporaryPassword : undefined;
  const [pendingDeactivate, setPendingDeactivate] = React.useState<IdentityUser | undefined>(
    undefined,
  );
  const [managingRoles, setManagingRoles] = React.useState<IdentityUser | undefined>(undefined);
  return (
    <Card aria-labelledby="identity-users-heading">
      <CardHeader>
        <CardTitle id="identity-users-heading">Users</CardTitle>
        <UsersScopeBadge scopeTenantId={scopeTenantId} tenants={scopeTenants} />
      </CardHeader>
      <CardContent className="grid gap-6">
        {scopeReady ? (
          <UsersBody
            usersState={usersState}
            callerErunUserId={callerErunUserId}
            onSetActive={setActive}
            onRequestDeactivate={setPendingDeactivate}
            onManageRoles={setManagingRoles}
          />
        ) : (
          <ScopedUsersStatus target={orgTarget} tenants={scopeTenants} tenantId={targetTenantId} />
        )}
        <EnrollForm
          token={token}
          ownTenantId={ownTenantId}
          tenantType={tenantType}
          enroll={enrollState}
          onEnroll={enroll}
        />
        <EnrollUserForm token={token} ownTenantId={ownTenantId} tenantType={tenantType} />
      </CardContent>
      {temporaryPassword !== undefined && enrollState.status === 'enrolled' && (
        <TemporaryPasswordDialog
          username={enrollState.result.idpUser.username}
          password={temporaryPassword}
          onDismiss={dismissTemporaryPassword}
        />
      )}
      {pendingDeactivate !== undefined && (
        <DeactivateUserDialog
          user={pendingDeactivate}
          onCancel={() => {
            setPendingDeactivate(undefined);
          }}
          onConfirm={setActive}
        />
      )}
      {managingRoles?.erunUserId !== undefined && (
        <UserRolesDialog
          username={managingRoles.username}
          userId={managingRoles.erunUserId}
          token={token}
          onClose={() => {
            setManagingRoles(undefined);
          }}
        />
      )}
    </Card>
  );
}
