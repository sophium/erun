import { Button } from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { InlineAlert } from '@/components/app/InlineAlert';
import type { JobView } from '@/components/app/ManageDialogJobs.helpers';
import type { UISelection } from '@/types';

import { CancelEnvironmentJob } from '../../../wailsjs/go/main/App';

// Cancel takes a confirm step, and reads "Cancel job" to avoid clashing with the dialog footer's Cancel.
export function JobCancelAction({
  job,
  selection,
  onCancelled,
}: {
  job: JobView;
  selection: UISelection;
  onCancelled: () => void;
}): React.ReactElement | null {
  const [confirming, setConfirming] = React.useState(false);
  const [cancelling, setCancelling] = React.useState(false);
  const [error, setError] = React.useState('');
  const label = job.name || job.id;

  if (job.state !== 'running') {
    return null;
  }

  const cancel = (): void => {
    setCancelling(true);
    setError('');
    CancelEnvironmentJob({
      tenant: selection.tenant,
      environment: selection.environment,
      id: job.id,
      signal: 'TERM',
    })
      .then(() => {
        setConfirming(false);
        onCancelled();
      })
      .catch((err: unknown) => {
        setError(readError(err));
      })
      .finally(() => {
        setCancelling(false);
      });
  };

  if (!confirming) {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          setConfirming(true);
        }}
        aria-label={`Cancel job ${label}`}
      >
        Cancel job
      </Button>
    );
  }

  return (
    <>
      <Button
        type="button"
        variant="destructive"
        size="sm"
        disabled={cancelling}
        onClick={cancel}
        aria-label={`Confirm cancelling ${label}`}
      >
        {cancelling && <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />}
        Confirm cancel
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={cancelling}
        onClick={() => {
          setConfirming(false);
        }}
      >
        Keep running
      </Button>
      {error && <InlineAlert>{error}</InlineAlert>}
    </>
  );
}
