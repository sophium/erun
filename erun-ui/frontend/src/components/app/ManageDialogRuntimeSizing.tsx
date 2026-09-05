import { Button } from 'erun-kit';
import { RefreshCw, TriangleAlert } from 'lucide-react';
import * as React from 'react';

import {
  useGetRuntimeSizingQuery,
  useResizeRuntimeToRecommendationMutation,
} from '@/app/api/environmentApi';
import { readError } from '@/app/errors';
import type { UISelection } from '@/types';
import type { UIRuntimeSizingAction, UIRuntimeSizingRecommendation } from '@/uiRuntimeTypes';

// RuntimeSizingField turns the environment's own standing sizing
// recommendation into a one-click action (erun#1320): applying it is
// `erun resize --apply-recommendation`, run inside the pod, so the operator
// never retypes the numbers this panel already shows. A resize rolls the
// pod, so it is refused while another worker holds the environment — that
// refusal names the holder and requires a deliberate second click
// ("Resize anyway") to override, never an implicit retry.
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
        // Stable wording from RuntimeResizeOccupancyError.Error()
        // (erun-common/runtime_resize.go) -- matched here only to decide
        // whether the explicit override affordance renders, never to
        // reinterpret or hide the error itself.
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

// Split out so the two failure presentations (a plain error, or the
// lease-held refusal with its explicit override affordance) don't add to
// RuntimeSizingField's own branching budget.
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
          className="flex items-start gap-1.5 text-xs leading-[1.35] text-amber-700 dark:text-amber-400"
          role="alert"
        >
          <TriangleAlert aria-hidden="true" className="mt-px size-3 shrink-0" />
          <span>{heldByOther}</span>
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
    return (
      <p className="text-xs leading-[1.35] text-destructive" role="alert">
        {readError(error)}
      </p>
    );
  }
  return null;
}

// A recommendation is worth acting on only when it is available, resolves to
// a real change (not a no-op), and actually names at least one action.
function runtimeSizingHasActions(data: UIRuntimeSizingRecommendation | undefined): boolean {
  return data?.available === true && data.noOp !== true && (data.actions?.length ?? 0) > 0;
}

// Owns the "what to show below the header" decision (loading, no
// recommendation, no-op, or the action list) so RuntimeSizingField's own
// complexity stays within budget.
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

// RuntimeSizingEvidence answers the question a bare verdict cannot: what was
// measured, over what window, and why it leads to this recommendation. Shown
// under the summary for every state a standing recommendation can resolve to
// -- including "Already sized as recommended", which otherwise reads as a
// dead end an operator cannot audit or argue with (see the "no dead ends"
// rule in root AGENTS.md). Rendered inline rather than behind a
// tooltip/popover since this is exactly the diagnostic detail the operator
// came to this panel to see, not a supplemental aside.
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
    <p className="text-xs leading-[1.35] text-muted-foreground" role="status">
      {message || 'No standing sizing recommendation is available for this environment yet.'}
    </p>
  );
}
