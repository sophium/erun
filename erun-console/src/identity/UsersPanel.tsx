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
  FieldLabel,
  Input,
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
import type { EnrollState, UsersState } from './controller';
import { useUsersController } from './controller';
import { EnrollUserForm } from './EnrollUserForm';
import { UserRolesDialog } from './UserRolesDialog';

// EnrollFeedback tells the operator which of the two enrollment paths the
// backend actually took: with mail configured, the identity provider emails
// the invite itself; without it, there is no mail path for that link to
// travel, so the temporary password shown below is the only way the new
// person signs in.
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

// TemporaryPasswordDialog shows the one-time credential enrollment returns
// when mail delivery is not configured. It never leaves a trace once closed:
// onDismiss (wired to both the "Done" button and clicking outside/the X)
// strips the password out of the controller's own state, so nothing keeps
// holding it after the operator has copied or noted it down.
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
  enroll,
  onEnroll,
}: {
  enroll: EnrollState;
  onEnroll: (input: EnrollIdentityUserInput) => void;
}): React.ReactElement {
  const [username, setUsername] = React.useState('');
  const [email, setEmail] = React.useState('');
  const [firstName, setFirstName] = React.useState('');
  const [lastName, setLastName] = React.useState('');
  const busy = enroll.status === 'enrolling';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onEnroll({
      username: username.trim(),
      email: email.trim(),
      firstName: firstName.trim() || undefined,
      lastName: lastName.trim() || undefined,
    });
  };

  return (
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="enroll-form-heading">
      <h3 id="enroll-form-heading" className="text-sm font-semibold text-foreground">
        Enroll a user
      </h3>
      <div className="grid gap-2">
        <FieldLabel htmlFor="enroll-username" required>
          Username
        </FieldLabel>
        <Input
          id="enroll-username"
          value={username}
          onChange={(e) => {
            setUsername(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="enroll-email" required>
          Email
        </FieldLabel>
        <Input
          id="enroll-email"
          type="email"
          value={email}
          onChange={(e) => {
            setEmail(e.target.value);
          }}
          required
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="enroll-first-name">First name</FieldLabel>
        <Input
          id="enroll-first-name"
          value={firstName}
          onChange={(e) => {
            setFirstName(e.target.value);
          }}
        />
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="enroll-last-name">Last name</FieldLabel>
        <Input
          id="enroll-last-name"
          value={lastName}
          onChange={(e) => {
            setLastName(e.target.value);
          }}
        />
      </div>
      <Button type="submit" disabled={busy} className="justify-self-start">
        {busy ? 'Enrolling…' : 'Enroll user'}
      </Button>
      <EnrollFeedback enroll={enroll} />
    </form>
  );
}

// DeactivateUserDialog gates the access-revoking half of the toggle behind an
// explicit confirmation (erun-ui/AGENTS.md's design-language record, #1419):
// deactivating blocks the user's next sign-in immediately, and the row it was
// clicked from looks identical to every other row, so a misclick is easy and
// its consequence is invisible until the person locked out reports it.
// Reactivating restores access rather than revoking it, so it stays a single
// click — only the deactivate half needs this gate.
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

// MembershipBadge tells the operator which of three states a row is in:
// an enrolled erun user of this tenant, a machine identity (the platform's
// own service accounts, never a tenant member), or an IdP-only account —
// someone who exists in the identity provider (most likely via
// self-registration) but has no erun access at all. Rendering all three
// identically is exactly the surface #1482 asks not to present.
function MembershipBadge({ user }: { user: IdentityUser }): React.ReactElement {
  if (user.isMachine) {
    return <span className="text-xs text-muted-foreground">Machine account</span>;
  }
  if (user.enrolled) {
    return <span className="text-xs font-medium text-foreground">Tenant member</span>;
  }
  return <span className="text-xs text-muted-foreground">IdP only, not enrolled</span>;
}

function UserRow({
  user,
  onSetActive,
  onRequestDeactivate,
  onManageRoles,
}: {
  user: IdentityUser;
  onSetActive: (externalId: string, active: boolean) => Promise<void>;
  onRequestDeactivate: (user: IdentityUser) => void;
  onManageRoles: (user: IdentityUser) => void;
}): React.ReactElement {
  const active = user.state === 'USER_STATE_ACTIVE';
  // The platform's own machine accounts (login-client, admin-sa) are the
  // IdP's own service identities, not people an operator manages here — a
  // one-click Deactivate beside them is a footgun on the sign-in path
  // itself, so they get no toggle at all rather than a confirmation dialog.
  const canToggle = !user.isMachine && (active || user.state === 'USER_STATE_INACTIVE');
  return (
    <TableRow>
      <TableCell className="font-medium text-foreground">{user.username}</TableCell>
      <TableCell>{user.email ?? ''}</TableCell>
      <TableCell>{user.state}</TableCell>
      <TableCell>
        <MembershipBadge user={user} />
      </TableCell>
      <TableCell>
        {/* Only an enrolled erun user has role assignments to manage — a
            machine account or an IdP-only, not-yet-enrolled account has no
            erun user row for a role to attach to. */}
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
  onSetActive,
  onRequestDeactivate,
  onManageRoles,
}: {
  users: IdentityUser[];
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
  onSetActive,
  onRequestDeactivate,
  onManageRoles,
}: {
  usersState: UsersState;
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
      onSetActive={onSetActive}
      onRequestDeactivate={onRequestDeactivate}
      onManageRoles={onManageRoles}
    />
  );
}

// UsersPanel is the console's IdP-identity administration surface: enroll a
// colleague (creates the IdP identity and the erun user mapping in one
// action), deactivate/reactivate an existing one, and manage which roles an
// enrolled user holds. Only rendered for an OPERATIONS tenant — see App.tsx.
// EnrollUserForm below is the separate, tenant-targetable enrollment path
// (POST /v1/users, erun#1744) that can put a first user into a tenant other
// than the caller's own — the one thing the IdP-creating form above cannot
// do, since it always creates the identity in the caller's own org.
export function UsersPanel({
  token,
  ownTenantId,
  tenantType,
}: {
  token: string;
  ownTenantId: string;
  tenantType: string;
}): React.ReactElement {
  const { usersState, enrollState, enroll, setActive, dismissTemporaryPassword } =
    useUsersController(token);
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
      </CardHeader>
      <CardContent className="grid gap-6">
        <UsersBody
          usersState={usersState}
          onSetActive={setActive}
          onRequestDeactivate={setPendingDeactivate}
          onManageRoles={setManagingRoles}
        />
        <EnrollForm enroll={enrollState} onEnroll={enroll} />
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
