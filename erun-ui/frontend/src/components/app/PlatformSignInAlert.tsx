import { Button } from 'erun-kit';
import { LoaderCircle, LogIn } from 'lucide-react';
import * as React from 'react';

import { signInAndRecover } from '@/app/cloudProviderThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { tenantNeedsSignIn } from '@/app/platformSignIn';

import { InlineAlert } from './InlineAlert';

// PlatformErrorAlert adds a sign-in action for an expired identity.
// onRecovered re-fetches whatever produced `message`, so a successful sign-in clears it.
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

// SignInAction uses the sidebar's login thunk so every surface updates, and calls onRecovered only on success.
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
  // Cleared on each attempt so a stale failure never lingers.
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
            // 'skipped' means another attempt was already running; nothing failed.
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
