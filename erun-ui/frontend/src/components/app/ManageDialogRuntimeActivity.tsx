import { Button } from 'erun-kit';
import { RefreshCw } from 'lucide-react';
import * as React from 'react';

import {
  useGetRuntimeActivityQuery,
  useReclaimRuntimeResourcesMutation,
} from '@/app/api/environmentApi';
import { readError } from '@/app/errors';
import { InlineAlert } from '@/components/app/InlineAlert';
import { RuntimePanelNotice } from '@/components/app/RuntimePanelNotice';
import type { UISelection } from '@/types';
import type { UIRuntimeProcessGroup } from '@/uiRuntimeTypes';

// RuntimeActivityField shows live sessions and memory holders; only an explicit click reclaims, never the agent session.
export function RuntimeActivityField({
  selection,
  disabled,
}: {
  selection: UISelection;
  disabled: boolean;
}): React.ReactElement {
  const { data, isFetching, refetch } = useGetRuntimeActivityQuery(selection);
  const [reclaim, reclaimState] = useReclaimRuntimeResourcesMutation();
  const [outcome, setOutcome] = React.useState('');
  const [failure, setFailure] = React.useState('');

  const runReclaim = (action: string): void => {
    setOutcome('');
    setFailure('');
    reclaim({ tenant: selection.tenant, environment: selection.environment, action })
      .unwrap()
      .then((result) => {
        setOutcome(result.message);
      })
      .catch((error: unknown) => {
        setFailure(readError(error));
      });
  };

  const busy = disabled || isFetching || reclaimState.isLoading;
  const groups = data?.processes ?? [];
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          Running in this environment
        </div>
        <Button
          id="environment-config-activity-refresh"
          type="button"
          size="sm"
          variant="ghost"
          className="h-7 px-2"
          aria-label="Refresh what the runtime is running"
          disabled={busy}
          onClick={() => void refetch()}
        >
          <RefreshCw aria-hidden="true" />
        </Button>
      </div>
      <RuntimeActivitySummary
        loading={isFetching && !data}
        available={data?.available === true}
        message={data?.message ?? ''}
      />
      {groups.length > 0 && (
        <ul className="grid gap-2">
          {groups.map((group) => (
            <ProcessGroupRow
              key={group.id}
              group={group}
              disabled={busy}
              onReclaim={() => {
                runReclaim(group.reclaim ?? '');
              }}
            />
          ))}
        </ul>
      )}
      <ReclaimOutcome outcome={outcome} failure={failure} />
    </div>
  );
}

// A failed reclaim is an alert that stays, with the button enabled for retry.
function ReclaimOutcome({
  outcome,
  failure,
}: {
  outcome: string;
  failure: string;
}): React.ReactElement | null {
  if (failure) {
    return <InlineAlert>{failure}</InlineAlert>;
  }
  if (!outcome) {
    return null;
  }
  return (
    <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
      {outcome}
    </p>
  );
}

function RuntimeActivitySummary({
  loading,
  available,
  message,
}: {
  loading: boolean;
  available: boolean;
  message: string;
}): React.ReactElement {
  if (loading) {
    return <p className="text-xs leading-[1.35] text-muted-foreground">Reading the runtime...</p>;
  }
  if (available) {
    return (
      <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
        {message || 'Open the environment to see what it is running.'}
      </p>
    );
  }
  // Shares the Runtime tab's failed-read rendering.
  return (
    <RuntimePanelNotice failure={message} empty="Open the environment to see what it is running." />
  );
}

function ProcessGroupRow({
  group,
  disabled,
  onReclaim,
}: {
  group: UIRuntimeProcessGroup;
  disabled: boolean;
  onReclaim: () => void;
}): React.ReactElement {
  const helpId = `environment-config-reclaim-${group.id}-help`;
  return (
    <li className="flex items-center justify-between gap-3">
      <span className="min-w-0 text-xs leading-[1.35]">
        <span className="font-medium">{group.label}</span>
        <span className="text-muted-foreground"> · {group.memory}</span>
      </span>
      {group.reclaim && (
        <>
          <Button
            id={`environment-config-reclaim-${group.id}`}
            type="button"
            size="sm"
            variant="outline"
            className="h-7 shrink-0 px-2"
            aria-describedby={helpId}
            disabled={disabled}
            onClick={onReclaim}
          >
            {group.reclaimLabel}
          </Button>
          <span className="sr-only" id={helpId}>
            Frees the memory these build leftovers hold. Your worktree, running sessions and the
            agent are untouched.
          </span>
        </>
      )}
    </li>
  );
}
