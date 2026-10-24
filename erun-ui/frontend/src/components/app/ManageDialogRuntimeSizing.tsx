import { Button } from 'erun-kit';
import { RefreshCw, TriangleAlert } from 'lucide-react';
import * as React from 'react';

import {
  useGetRuntimeSizingQuery,
  useResizeRuntimeToRecommendationMutation,
} from '@/app/api/environmentApi';
import { readError } from '@/app/errors';
import { InlineAlert } from '@/components/app/InlineAlert';
import { RuntimePanelNotice } from '@/components/app/RuntimePanelNotice';
import type { UISelection } from '@/types';
import type { UIRuntimeSizingAction, UIRuntimeSizingRecommendation } from '@/uiRuntimeTypes';

// The reader reports empty and failed both as available=false; only this copy tells them apart.
const SIZING_EMPTY_STATE =
  'No standing sizing recommendation is available for this environment yet.';

// RuntimeSizingField applies the standing recommendation; a lease-held refusal needs an explicit "Resize anyway".
export function RuntimeSizingField({
  selection,
  disabled,
}: {
  selection: UISelection;
  disabled: boolean;
}): React.ReactElement {
  const { data, isFetching, refetch } = useGetRuntimeSizingQuery(selection);
  const [resize, resizeState] = useResizeRuntimeToRecommendationMutation();
  const [heldByOther, setHeldByOther] = React.useState<string | null>(null);
  const busy = disabled || isFetching || resizeState.isLoading;

  const applyResize = (overrideLease: boolean) => {
    setHeldByOther(null);
    resize({ selection, overrideLease })
      .unwrap()
      .catch((error: unknown) => {
        const message = readError(error);
        // Matches RuntimeResizeOccupancyError's wording only to decide whether to offer the override.
        setHeldByOther(message.includes('pass the override') ? message : null);
      });
  };

  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          Sizing recommendation
        </div>
        <Button
          id="environment-config-sizing-refresh"
          type="button"
          size="sm"
          variant="ghost"
          className="h-7 px-2"
          aria-label="Refresh this environment's sizing recommendation"
          disabled={busy}
          onClick={() => void refetch()}
        >
          <RefreshCw aria-hidden="true" className={isFetching ? 'animate-spin' : undefined} />
        </Button>
      </div>
      <RuntimeSizingRecommendationBody
        loading={isFetching && !data}
        data={data}
        busy={busy}
        onResize={() => {
          applyResize(false);
        }}
      />
      <RuntimeSizingEvidence verdicts={data?.verdicts ?? []} evidence={data?.evidence} />
      <RuntimeSizingResizeOutcome
        isError={resizeState.isError}
        error={resizeState.error}
        heldByOther={heldByOther}
        busy={busy}
        onOverride={() => {
          applyResize(true);
        }}
      />
    </div>
  );
}

function RuntimeSizingResizeOutcome({
  isError,
  error,
  heldByOther,
  busy,
  onOverride,
}: {
  isError: boolean;
  error: unknown;
  heldByOther: string | null;
  busy: boolean;
  onOverride: () => void;
}): React.ReactElement | null {
  if (heldByOther) {
    return (
      <div className="grid gap-2 rounded-[var(--radius)] border border-amber-300 bg-amber-50 p-2 dark:border-amber-800 dark:bg-amber-950">
        <p
          className="flex items-start gap-1.5 text-xs leading-[1.35] text-amber-700 dark:text-amber-400 [overflow-wrap:anywhere]"
          role="alert"
        >
          <TriangleAlert aria-hidden="true" className="mt-px size-3 shrink-0" />
          {/* The holder name can be long and unbreakable. */}
          <span className="min-w-0">{heldByOther}</span>
        </p>
        <Button
          id="environment-config-sizing-override"
          type="button"
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={onOverride}
        >
          Resize anyway
        </Button>
      </div>
    );
  }
  if (isError) {
    return <InlineAlert>{readError(error)}</InlineAlert>;
  }
  return null;
}

function runtimeSizingHasActions(data: UIRuntimeSizingRecommendation | undefined): boolean {
  return data?.available === true && data.noOp !== true && (data.actions?.length ?? 0) > 0;
}

function RuntimeSizingRecommendationBody({
  loading,
  data,
  busy,
  onResize,
}: {
  loading: boolean;
  data: UIRuntimeSizingRecommendation | undefined;
  busy: boolean;
  onResize: () => void;
}): React.ReactElement {
  const actions = data?.actions ?? [];
  const showActions = runtimeSizingHasActions(data);
  return (
    <>
      <RuntimeSizingSummary
        loading={loading}
        available={data?.available === true}
        noOp={data?.noOp === true}
        message={data?.message ?? ''}
      />
      {showActions && <RuntimeSizingActions actions={actions} busy={busy} onResize={onResize} />}
    </>
  );
}

function RuntimeSizingActions({
  actions,
  busy,
  onResize,
}: {
  actions: UIRuntimeSizingAction[];
  busy: boolean;
  onResize: () => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <ul className="grid gap-1 text-xs leading-[1.35] text-foreground">
        {actions.map((action) => (
          <li key={action.resource}>
            {action.resource}: {action.from} &rarr; {action.to}
          </li>
        ))}
      </ul>
      <Button
        id="environment-config-sizing-apply"
        type="button"
        size="sm"
        disabled={busy}
        onClick={onResize}
      >
        Resize to this
      </Button>
    </div>
  );
}

// RuntimeSizingEvidence shows what was measured and why, so no verdict is a dead end.
function RuntimeSizingEvidence({
  verdicts,
  evidence,
}: {
  verdicts: string[];
  evidence?: string;
}): React.ReactElement | null {
  if (verdicts.length === 0 && !evidence) {
    return null;
  }
  return (
    <div
      className="grid gap-1 rounded-[var(--radius)] bg-muted/40 p-2 text-[11px] leading-[1.4] text-muted-foreground"
      role="status"
    >
      {verdicts.length > 0 && (
        <ul className="grid gap-0.5">
          {verdicts.map((verdict) => (
            <li key={verdict}>{verdict}</li>
          ))}
        </ul>
      )}
      {evidence && <p>{evidence}</p>}
    </div>
  );
}

function RuntimeSizingSummary({
  loading,
  available,
  noOp,
  message,
}: {
  loading: boolean;
  available: boolean;
  noOp: boolean;
  message: string;
}): React.ReactElement {
  if (loading) {
    return (
      <p className="text-xs leading-[1.35] text-muted-foreground">Reading the recommendation...</p>
    );
  }
  if (available && noOp) {
    return (
      <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
        Already sized as recommended.
      </p>
    );
  }
  if (available) {
    return (
      <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
        This environment has a standing recommendation:
      </p>
    );
  }
  return (
    <RuntimePanelNotice
      failure={message === SIZING_EMPTY_STATE ? '' : message}
      empty={message || SIZING_EMPTY_STATE}
    />
  );
}
