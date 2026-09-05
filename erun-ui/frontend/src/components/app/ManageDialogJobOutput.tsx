import { Button } from 'erun-kit';
import * as React from 'react';

import { readError } from '@/app/errors';
import { InlineAlert } from '@/components/app/InlineAlert';
import type { JobView } from '@/components/app/ManageDialogJobs.helpers';
import type { UISelection } from '@/types';

import { ReadEnvironmentJobOutput } from '../../../wailsjs/go/main/App';

// Output is read a page at a time and continued from the offset the previous
// read returned, so a long-running job's log does not have to be held whole in
// memory to be readable, and a second read continues rather than repeats.
export function JobOutputView({
  job,
  selection,
}: {
  job: JobView;
  selection: UISelection;
}): React.ReactElement {
  const [text, setText] = React.useState('');
  const [nextOffset, setNextOffset] = React.useState(0);
  const [hasMore, setHasMore] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState('');
  const [loaded, setLoaded] = React.useState(false);

  const read = React.useCallback(
    (offset: number) => {
      setLoading(true);
      setError('');
      ReadEnvironmentJobOutput({
        tenant: selection.tenant,
        environment: selection.environment,
        id: job.id,
        offset,
        maxBytes: 0,
      })
        .then((page) => {
          setText((previous) => (offset === 0 ? page.output : previous + page.output));
          setNextOffset(page.nextOffset);
          setHasMore(page.hasMore);
          setLoaded(true);
        })
        .catch((err: unknown) => {
          setError(readError(err));
        })
        .finally(() => {
          setLoading(false);
        });
    },
    [job.id, selection],
  );

  React.useEffect(() => {
    read(0);
  }, [read]);

  if (error) {
    return <InlineAlert>Could not read this job&apos;s output. {error}</InlineAlert>;
  }
  if (!loaded && loading) {
    return (
      <p className="text-[12px] text-muted-foreground" data-testid="manage-jobs-output-loading">
        Reading output…
      </p>
    );
  }
  if (loaded && text === '') {
    // Distinct from "not read yet" and from an error: the job genuinely printed
    // nothing, which for a quiet successful job is the expected case.
    return (
      <p className="text-[12px] text-muted-foreground" data-testid="manage-jobs-output-empty">
        This job produced no output.
      </p>
    );
  }
  return (
    <div className="grid gap-1.5">
      <pre
        className="max-h-64 overflow-auto rounded-[var(--radius)] border border-border bg-muted/40 px-2.5 py-2 font-mono text-[12px] leading-[1.45] whitespace-pre-wrap [overflow-wrap:anywhere]"
        data-testid="manage-jobs-output"
      >
        {text}
      </pre>
      {hasMore && (
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={loading}
            onClick={() => {
              read(nextOffset);
            }}
            aria-label={`Read more output for ${job.name || job.id}`}
          >
            {loading ? 'Reading…' : 'Read more'}
          </Button>
        </div>
      )}
    </div>
  );
}
