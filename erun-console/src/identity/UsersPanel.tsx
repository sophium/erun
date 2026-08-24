import * as React from 'react';

import type { EnrollIdentityUserInput, IdentityUser } from './client';
import type { EnrollState, UsersState } from './controller';
import { useUsersController } from './controller';

function EnrollFeedback({ enroll }: { enroll: EnrollState }): React.ReactElement | null {
  if (enroll.status === 'enrolled') {
    if (enroll.result.error !== undefined) {
      return (
        <p className="identity-feedback identity-feedback--error" role="alert">
          {enroll.result.idpUser.username} was created in the identity provider (id{' '}
          {enroll.result.idpUser.id}), but could not be enrolled as an erun user:{' '}
          {enroll.result.error}
        </p>
      );
    }
    return (
      <p className="identity-feedback identity-feedback--ok" role="status">
        Enrolled {enroll.result.idpUser.username}. They will receive an email to complete sign-in.
      </p>
    );
  }
  if (enroll.status === 'error') {
    return (
      <p className="identity-feedback identity-feedback--error" role="alert">
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
    <form className="identity-form" onSubmit={submit} aria-labelledby="enroll-form-heading">
      <h3 id="enroll-form-heading">Enroll a user</h3>
      <label htmlFor="enroll-username">Username</label>
      <input
        id="enroll-username"
        value={username}
        onChange={(e) => {
          setUsername(e.target.value);
        }}
        required
      />
      <label htmlFor="enroll-email">Email</label>
      <input
        id="enroll-email"
        type="email"
        value={email}
        onChange={(e) => {
          setEmail(e.target.value);
        }}
        required
      />
      <label htmlFor="enroll-first-name">First name</label>
      <input
        id="enroll-first-name"
        value={firstName}
        onChange={(e) => {
          setFirstName(e.target.value);
        }}
      />
      <label htmlFor="enroll-last-name">Last name</label>
      <input
        id="enroll-last-name"
        value={lastName}
        onChange={(e) => {
          setLastName(e.target.value);
        }}
      />
      <button type="submit" disabled={busy}>
        {busy ? 'Enrolling…' : 'Enroll user'}
      </button>
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
    <tr>
      <td>{user.username}</td>
      <td>{user.email ?? ''}</td>
      <td>{user.state}</td>
      <td>
        {canToggle && (
          <button
            type="button"
            onClick={() => {
              onSetActive(user.id, !active);
            }}
          >
            {active ? 'Deactivate' : 'Reactivate'}
          </button>
        )}
      </td>
    </tr>
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
    return <p className="identity-empty">No users enrolled yet.</p>;
  }
  return (
    <table className="identity-users-table">
      <thead>
        <tr>
          <th>Username</th>
          <th>Email</th>
          <th>State</th>
          <th />
        </tr>
      </thead>
      <tbody>
        {users.map((user) => (
          <UserRow key={user.id} user={user} onSetActive={onSetActive} />
        ))}
      </tbody>
    </table>
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
    return <p role="status">Loading users…</p>;
  }
  if (usersState.status === 'error') {
    return (
      <p className="identity-feedback identity-feedback--error" role="alert">
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
    <section className="identity-users-panel" aria-labelledby="identity-users-heading">
      <h2 id="identity-users-heading">Users</h2>
      <UsersBody usersState={usersState} onSetActive={setActive} />
      <EnrollForm enroll={enrollState} onEnroll={enroll} />
    </section>
  );
}
