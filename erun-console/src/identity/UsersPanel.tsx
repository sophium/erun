import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
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
import { Users } from 'lucide-react';
import * as React from 'react';

import type { EnrollIdentityUserInput, IdentityUser } from '../app/api/identityApi';
import type { EnrollState, UsersState } from './controller';
import { useUsersController } from './controller';

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
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Enrolled {enroll.result.idpUser.username}. They will receive an email to complete sign-in.
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

function UserRow({
  user,
  onSetActive,
}: {
  user: IdentityUser;
  onSetActive: (externalId: string, active: boolean) => void;
}): React.ReactElement {
  const active = user.state === 'USER_STATE_ACTIVE';
  const canToggle = active || user.state === 'USER_STATE_INACTIVE';
  return (
    <TableRow>
      <TableCell className="font-medium text-foreground">{user.username}</TableCell>
      <TableCell>{user.email ?? ''}</TableCell>
      <TableCell>{user.state}</TableCell>
      <TableCell>
        {canToggle && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              onSetActive(user.id, !active);
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
}: {
  users: IdentityUser[];
  onSetActive: (externalId: string, active: boolean) => void;
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
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {users.map((user) => (
          <UserRow key={user.id} user={user} onSetActive={onSetActive} />
        ))}
      </TableBody>
    </Table>
  );
}

function UsersBody({
  usersState,
  onSetActive,
}: {
  usersState: UsersState;
  onSetActive: (externalId: string, active: boolean) => void;
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
  return <UsersTable users={usersState.users} onSetActive={onSetActive} />;
}

// UsersPanel is the console's IdP-identity administration surface (issue
// #1209): enroll a colleague (creates the IdP identity and the erun user
// mapping in one action) and deactivate/reactivate an existing one. Only
// rendered for an OPERATIONS tenant — see App.tsx.
export function UsersPanel({ token }: { token: string }): React.ReactElement {
  const { usersState, enrollState, enroll, setActive } = useUsersController(token);
  return (
    <Card aria-labelledby="identity-users-heading">
      <CardHeader>
        <CardTitle id="identity-users-heading">Users</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-6">
        <UsersBody usersState={usersState} onSetActive={setActive} />
        <EnrollForm enroll={enrollState} onEnroll={enroll} />
      </CardContent>
    </Card>
  );
}
