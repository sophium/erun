import { Button, Card, CardContent, CardHeader, CardTitle, FieldLabel, Input } from 'erun-kit';
import * as React from 'react';

import { useSetInviteRequestRateLimitMutation } from '../app/api/requestsApi';
import { queryErrorMessage } from '../app/queryError';
import { describeRateLimitWindow } from './describeRateLimitWindow';

// Rendered only for an OPERATIONS tenant.
export function RateLimitPanel({
  token,
  currentWindowSeconds,
}: {
  token: string;
  currentWindowSeconds: number;
}): React.ReactElement {
  const [value, setValue] = React.useState(String(currentWindowSeconds));
  const [setRateLimit, setRateLimitState] = useSetInviteRequestRateLimitMutation();
  const busy = setRateLimitState.isLoading;

  // A fresh currentWindowSeconds always overwrites the local draft.
  React.useEffect(() => {
    setValue(String(currentWindowSeconds));
  }, [currentWindowSeconds]);

  const parsed = Number(value);
  const invalid = value.trim() === '' || !Number.isFinite(parsed) || parsed < 1;

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    if (invalid) {
      return;
    }
    void setRateLimit({ token, windowSeconds: Math.round(parsed) });
  };

  return (
    <Card aria-labelledby="rate-limit-heading">
      <CardHeader>
        <CardTitle id="rate-limit-heading">Invite-request rate limit</CardTitle>
      </CardHeader>
      <CardContent>
        <form className="grid max-w-sm gap-3" onSubmit={submit}>
          <div className="grid gap-2">
            <FieldLabel htmlFor="rate-limit-window" required>
              Window (seconds)
            </FieldLabel>
            <Input
              id="rate-limit-window"
              type="number"
              min={1}
              value={value}
              onChange={(e) => {
                setValue(e.target.value);
              }}
              required
            />
            {invalid ? (
              <p className="text-sm text-destructive" role="alert">
                The window must be at least 1 second — the limiter cannot be disabled by setting it
                to zero.
              </p>
            ) : (
              <p className="text-sm text-muted-foreground" role="status">
                {describeRateLimitWindow(parsed)}
              </p>
            )}
          </div>
          <Button type="submit" disabled={busy || invalid} className="justify-self-start">
            {busy ? 'Saving…' : 'Save'}
          </Button>
          {setRateLimitState.isSuccess && (
            <p className="text-sm text-muted-foreground" role="status">
              Rate limit updated. Requests already pending stay queued and approvable.
            </p>
          )}
          {setRateLimitState.isError && (
            <p className="text-sm text-destructive" role="alert">
              Could not update rate limit: {queryErrorMessage(setRateLimitState.error)}
            </p>
          )}
        </form>
      </CardContent>
    </Card>
  );
}
