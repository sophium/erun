import { Button } from 'erun-kit';
import { LoaderCircle, LogIn } from 'lucide-react';
import * as React from 'react';

import { loginPrimaryCloudProvider } from '@/app/cloudProviderThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { tenantNeedsSignIn } from '@/app/platformSignIn';

import { InlineAlert } from './InlineAlert';

// PlatformErrorAlert is InlineAlert plus the one thing #1390 found four
// separate dead ends missing: when the platform reports the signed-in
// identity is no longer good, the fix is a "Log in" click away — the same
// control the sidebar's cloud alias row already offers (#1390) — so it
// renders right beside the message instead of leaving the operator to find
// that control on their own. Every other message stays exactly what it was:
// InlineAlert with no action, which is correct for a remedy only a person can
// carry out (e.g. "ask an administrator").
export function PlatformErrorAlert({
  message,
  alias,
}: {
  message: string;
  alias: string;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <InlineAlert>{message}</InlineAlert>
      {tenantNeedsSignIn(message) && alias !== '' && <SignInAction alias={alias} />}
    </div>
  );
}

// SignInAction dispatches the exact same thunk the sidebar's cloud alias
// login button uses, so a re-authenticated alias updates every surface
// reading it — sidebar included — not just the one that prompted the login.
function SignInAction({ alias }: { alias: string }): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = useAppSelector(
    (state) => (state.sidebar.sidebarCloudAliasBusyByAlias[alias] ?? '') !== '',
  );
  return (
    <Button
      type="button"
      size="sm"
      disabled={busy}
      onClick={() => {
        void dispatch(loginPrimaryCloudProvider(alias));
      }}
    >
      {busy ? (
        <LoaderCircle className="animate-spin" aria-hidden="true" />
      ) : (
        <LogIn aria-hidden="true" />
      )}
      {busy ? 'Logging in...' : 'Log in'}
    </Button>
  );
}
