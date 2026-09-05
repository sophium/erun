import { Button, Card, CardContent, CardHeader, CardTitle, FieldLabel, Input } from 'erun-kit';
import * as React from 'react';

import { useAcceptInviteMutation } from '../app/api/identityApi';
import { describeQueryError } from '../app/queryError';

// describeAcceptError turns the invite-accept endpoint's status code into
// the exact, distinct message #1483 asks for: an expired or consumed link
// says so plainly rather than a generic failure, since "try again" is not
// the right recovery for any of these three states.
function describeAcceptError(error: unknown): string {
  const { status, message } = describeQueryError(error);
  switch (status) {
    case 404:
      return 'This invite link is not valid. Ask whoever invited you to send a new one.';
    case 410:
      return 'This invite link has expired or has already been used. Ask whoever invited you to send a new one.';
    case 400:
      return 'The email address you entered does not match the one this invite was sent to.';
    default:
      return message;
  }
}

type AcceptState =
  | { status: 'idle' }
  | { status: 'submitting' }
  | { status: 'succeeded' }
  | { status: 'partial'; message: string }
  | { status: 'error'; message: string };

function TextField({
  id,
  label,
  type = 'text',
  required,
  value,
  onChange,
}: {
  id: string;
  label: string;
  type?: string;
  required?: boolean;
  value: string;
  onChange: (value: string) => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <FieldLabel htmlFor={id} required={required}>
        {label}
      </FieldLabel>
      <Input
        id={id}
        type={type}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        required={required}
      />
    </div>
  );
}

// useAcceptInviteFields holds the form's five inputs as one unit, so
// AcceptInviteForm only needs one hook call and one destructure instead of
// five repeated pairs — mirrors OrgSettingsPanel's usePasswordComplexityFields.
function useAcceptInviteFields(): {
  username: string;
  email: string;
  firstName: string;
  lastName: string;
  password: string;
  setUsername: (value: string) => void;
  setEmail: (value: string) => void;
  setFirstName: (value: string) => void;
  setLastName: (value: string) => void;
  setPassword: (value: string) => void;
} {
  const [username, setUsername] = React.useState('');
  const [email, setEmail] = React.useState('');
  const [firstName, setFirstName] = React.useState('');
  const [lastName, setLastName] = React.useState('');
  const [password, setPassword] = React.useState('');
  return {
    username,
    email,
    firstName,
    lastName,
    password,
    setUsername,
    setEmail,
    setFirstName,
    setLastName,
    setPassword,
  };
}

function AcceptInviteForm({
  token,
  onResult,
}: {
  token: string;
  onResult: (state: AcceptState) => void;
}): React.ReactElement {
  const fields = useAcceptInviteFields();
  const [acceptInvite, { isLoading }] = useAcceptInviteMutation();

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onResult({ status: 'submitting' });
    acceptInvite({
      token,
      username: fields.username.trim(),
      email: fields.email.trim() || undefined,
      firstName: fields.firstName.trim() || undefined,
      lastName: fields.lastName.trim() || undefined,
      password: fields.password,
    })
      .unwrap()
      .then((result) => {
        if (result.error !== undefined) {
          onResult({ status: 'partial', message: result.error });
          return;
        }
        onResult({ status: 'succeeded' });
      })
      .catch((error: unknown) => {
        onResult({ status: 'error', message: describeAcceptError(error) });
      });
  };

  return (
    <form className="grid gap-3" onSubmit={submit} aria-labelledby="accept-invite-heading">
      <TextField
        id="accept-username"
        label="Username"
        required
        value={fields.username}
        onChange={fields.setUsername}
      />
      <TextField
        id="accept-email"
        label="Email"
        type="email"
        value={fields.email}
        onChange={fields.setEmail}
      />
      <TextField
        id="accept-first-name"
        label="First name"
        value={fields.firstName}
        onChange={fields.setFirstName}
      />
      <TextField
        id="accept-last-name"
        label="Last name"
        value={fields.lastName}
        onChange={fields.setLastName}
      />
      <TextField
        id="accept-password"
        label="Password"
        type="password"
        required
        value={fields.password}
        onChange={fields.setPassword}
      />
      <Button type="submit" disabled={isLoading} className="justify-self-start">
        {isLoading ? 'Creating account…' : 'Create account'}
      </Button>
    </form>
  );
}

function AcceptInviteResultMessage({ state }: { state: AcceptState }): React.ReactElement | null {
  if (state.status === 'succeeded') {
    return (
      <p className="text-sm text-foreground" role="status">
        Your account is ready.{' '}
        <a className="underline" href="/">
          Sign in
        </a>{' '}
        to continue.
      </p>
    );
  }
  if (state.status === 'partial') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Your account was created in the identity provider, but could not be finished:{' '}
        {state.message}. Ask an operator to complete your enrollment.
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        {state.message}
      </p>
    );
  }
  return null;
}

// AcceptInvitePage is the unauthenticated landing page for an invite link
// (#1483): the invitee has no bearer token yet, so this renders standalone
// before App.tsx's normal OIDC-gated flow ever starts. See App.tsx for the
// path check that routes here instead of dispatching resolveAuth().
export function AcceptInvitePage({ token }: { token: string }): React.ReactElement {
  const [state, setState] = React.useState<AcceptState>({ status: 'idle' });
  const done = state.status === 'succeeded' || state.status === 'partial';

  return (
    <div className="flex min-h-dvh items-center justify-center bg-background p-6 text-foreground">
      <Card className="w-full max-w-md" aria-labelledby="accept-invite-heading">
        <CardHeader>
          <CardTitle id="accept-invite-heading">You've been invited</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4">
          {token === '' ? (
            <p className="text-sm text-destructive" role="alert">
              This link is missing its invite token. Ask whoever invited you to send it again.
            </p>
          ) : (
            <>
              {!done && <AcceptInviteForm token={token} onResult={setState} />}
              <AcceptInviteResultMessage state={state} />
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
