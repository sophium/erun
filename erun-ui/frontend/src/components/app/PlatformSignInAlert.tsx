import { Button } from 'erun-kit';
import { LoaderCircle, LogIn } from 'lucide-react';
import * as React from 'react';

import { signInAndRecover } from '@/app/cloudProviderThunks';
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
//
// A successful sign-in on its own leaves this exact surface rendered from the
// stale state that produced it (#1392) — the operator sees the identical
// error and button after the problem is already fixed. onRecovered is the
// caller's own re-fetch or error-clear for whatever produced `message`, so a
// successful sign-in actually resolves what's on screen instead of just
// updating the alias elsewhere.
export function PlatformErrorAlert({
  message,
  alias,
  onRecovered,
}: {
  message: string;
  alias: string;
  onRecovered: () => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <InlineAlert>{message}</InlineAlert>
      {tenantNeedsSignIn(message) && alias !== '' && (
        <SignInAction alias={alias} onRecovered={onRecovered} />
      )}
    </div>
  );
}

// SignInAction dispatches signInAndRecover, which itself dispatches the exact
// same login thunk the sidebar's cloud alias login button uses, so a
// re-authenticated alias updates every surface reading it — sidebar included
// — not just the one that prompted the login. It only calls onRecovered once
// the sign-in itself reports success; a failed sign-in says so right here
// instead of silently leaving the same message and button on screen, which is
// the identical trap one level down (#1392).
// SignInAction is also reused directly by TenantPlatformState.tsx for the
// dashboard's own not-signed-in state, which needs the exact same sign-in +
// recover behavior beside a different (typed-state) message.
export function SignInAction({
  alias,
  onRecovered,
}: {
  alias: string;
  onRecovered: () => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const busy = useAppSelector(
    (state) => (state.sidebar.sidebarCloudAliasBusyByAlias[alias] ?? '') !== '',
  );
  // The real failure reason, not a generic sentence — updatePrimaryCloudProvider
  // already computed one via readError and only threw it away here (#1392
  // review). "" clears on every new attempt so a stale failure never lingers
  // beside a click that has not resolved yet.
  const [signInError, setSignInError] = React.useState('');
  return (
    <div className="grid gap-2">
      <Button
        type="button"
        size="sm"
        disabled={busy}
        onClick={() => {
          setSignInError('');
          void (async () => {
            const outcome = await dispatch(signInAndRecover(alias, onRecovered));
            // 'skipped' means this click found the alias already busy with
            // another attempt (or blank) — nothing ran, so nothing failed;
            // rendering an error here would blame a click that never happened
            // (#1392 review, second defect).
            if (outcome.status === 'failed') {
              setSignInError(outcome.message);
            }
          })();
        }}
      >
        {busy ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <LogIn aria-hidden="true" />
        )}
        {busy ? 'Logging in...' : 'Log in'}
      </Button>
      {signInError && <InlineAlert>{signInError}</InlineAlert>}
    </div>
  );
}
