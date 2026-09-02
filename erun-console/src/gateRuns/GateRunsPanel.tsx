import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  FieldLabel,
  Input,
  SelectField,
  StatusBadge,
  type StatusBadgeTone,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from 'erun-kit';
import { ListChecks, Search } from 'lucide-react';
import * as React from 'react';

import type { GateRun, GateRunFilter, GateRunStatus } from '../app/api/gateRunsApi';
import { useGetGateRunQuery } from '../app/api/gateRunsApi';
import { queryErrorMessage } from '../app/queryError';
import type { GateRunsState } from './controller';
import { useGateRunsController } from './controller';

// GateRunsPanel answers, without the operator knowing any job ids: what is
// being gated right now, and what did recent gates decide (erun#1932). It
// is the console's counterpart to `erun gate list`/`erun gate show`.
//
// The one thing this must get right: INCONCLUSIVE is not a failure. It
// exists precisely because a wrapper hitting its own timeout, or an
// environment fault, is not a verdict on the change -- so it renders with
// its own `warning` tone, visibly distinct from FAILED's `destructive` tone,
// never folded into the same red state (see erun-backend-api/AGENTS.md's
// "Gate Runs").
const STATUS_TONES: Record<GateRunStatus, StatusBadgeTone> = {
  RUNNING: 'in-progress',
  PASSED: 'success',
  FAILED: 'destructive',
  INCONCLUSIVE: 'warning',
};

function gateRunStatusTone(status: GateRunStatus): StatusBadgeTone {
  return STATUS_TONES[status];
}

// shortCommit shows a git-familiar 8-character prefix rather than the full
// 40-character SHA, which would otherwise dominate the row.
function shortCommit(commit: string | undefined): string {
  if (commit === undefined || commit === '') {
    return '—';
  }
  return commit.slice(0, 8);
}

const STATUS_FILTER_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: 'All statuses' },
  { value: 'RUNNING', label: 'Running' },
  { value: 'PASSED', label: 'Passed' },
  { value: 'FAILED', label: 'Failed' },
  { value: 'INCONCLUSIVE', label: 'Inconclusive' },
];

function GateRunBranches({ run }: { run: GateRun }): React.ReactElement {
  return (
    <div className="grid gap-0.5">
      <span className="font-medium text-foreground">{run.sourceBranch}</span>
      <span className="text-xs text-muted-foreground">→ {run.targetBranch}</span>
    </div>
  );
}

// GateRunOutcome carries the load-bearing "why" for a red run: which step
// failed, and where to read it -- the log ref is free text (a job id, URL,
// or path), so it's rendered as plain text rather than assumed to be a link.
function GateRunOutcome({ run }: { run: GateRun }): React.ReactElement {
  if (run.status !== 'FAILED') {
    return <span className="text-muted-foreground">—</span>;
  }
  return (
    <div className="grid gap-0.5 text-sm">
      <span className="text-foreground">{run.failingStep ?? 'unknown step'}</span>
      {run.logRef !== undefined && (
        <span className="break-all text-xs text-muted-foreground">{run.logRef}</span>
      )}
    </div>
  );
}

function GateRunRow({ run }: { run: GateRun }): React.ReactElement {
  return (
    <TableRow>
      <TableCell>
        <StatusBadge tone={gateRunStatusTone(run.status)} label={run.status} />
      </TableCell>
      <TableCell>
        <GateRunBranches run={run} />
      </TableCell>
      <TableCell className="font-mono text-xs">{shortCommit(run.mergeCommit)}</TableCell>
      <TableCell>{run.reviewName ?? <span className="text-muted-foreground">—</span>}</TableCell>
      <TableCell>
        <GateRunOutcome run={run} />
      </TableCell>
      <TableCell className="text-sm text-muted-foreground">
        {new Date(run.updatedAt).toLocaleString()}
      </TableCell>
    </TableRow>
  );
}

function GateRunsTable({ runs }: { runs: GateRun[] }): React.ReactElement {
  if (runs.length === 0) {
    return (
      <EmptyState
        icon={<ListChecks />}
        heading="No gate runs match this filter."
        body="A gate run appears here the moment an environment starts gating a prospective merge."
      />
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Status</TableHead>
          <TableHead>Branch</TableHead>
          <TableHead>Merge commit</TableHead>
          <TableHead>Review</TableHead>
          <TableHead>Outcome</TableHead>
          <TableHead>Updated</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.map((run) => (
          <GateRunRow key={run.gateRunId} run={run} />
        ))}
      </TableBody>
    </Table>
  );
}

function GateRunsBody({ state }: { state: GateRunsState }): React.ReactElement {
  if (state.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading gate runs…
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load gate runs: {state.message}
      </p>
    );
  }
  return <GateRunsTable runs={state.runs} />;
}

function GateRunDetailRow({
  label,
  value,
}: {
  label: string;
  value: React.ReactNode;
}): React.ReactElement {
  return (
    <div className="flex items-center justify-between border-b border-border py-2 text-sm last:border-b-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-medium text-foreground">{value}</dd>
    </div>
  );
}

function GateRunDetail({ run }: { run: GateRun }): React.ReactElement {
  return (
    <dl className="rounded-[var(--radius)] border border-border p-3">
      <GateRunDetailRow
        label="Status"
        value={<StatusBadge tone={gateRunStatusTone(run.status)} label={run.status} />}
      />
      <GateRunDetailRow label="Branch" value={<GateRunBranches run={run} />} />
      <GateRunDetailRow
        label="Source commit"
        value={<span className="font-mono text-xs">{shortCommit(run.sourceCommit)}</span>}
      />
      <GateRunDetailRow
        label="Merge commit"
        value={<span className="font-mono text-xs">{shortCommit(run.mergeCommit)}</span>}
      />
      <GateRunDetailRow label="Review" value={run.reviewName ?? run.reviewId ?? '—'} />
      <GateRunDetailRow label="Outcome" value={<GateRunOutcome run={run} />} />
      <GateRunDetailRow label="Updated" value={new Date(run.updatedAt).toLocaleString()} />
    </dl>
  );
}

// GateRunLookup is `erun gate show`'s console counterpart: paste a gate run
// id (from a `logRef`, a CLI/MCP report, or a linked review) and jump
// straight to that one record without scanning the whole queue below.
function GateRunLookup({ token }: { token: string }): React.ReactElement {
  const [input, setInput] = React.useState('');
  const [gateRunId, setGateRunId] = React.useState('');
  const query = useGetGateRunQuery({ token, gateRunId }, { skip: gateRunId === '' });

  return (
    <form
      className="grid gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        setGateRunId(input.trim());
      }}
    >
      <FieldLabel htmlFor="gate-run-lookup">Look up a gate run by id</FieldLabel>
      <div className="flex max-w-md gap-2">
        <Input
          id="gate-run-lookup"
          value={input}
          placeholder="gr_01H..."
          onChange={(e) => {
            setInput(e.target.value);
          }}
        />
        <Button type="submit" variant="outline" disabled={input.trim() === ''}>
          <Search aria-hidden="true" />
          Look up
        </Button>
      </div>
      {gateRunId !== '' && query.isError && (
        <p className="text-sm text-destructive" role="alert">
          Could not find gate run {gateRunId}: {queryErrorMessage(query.error)}
        </p>
      )}
      {query.data !== undefined && <GateRunDetail run={query.data} />}
    </form>
  );
}

// GateRunsPanel is every tenant's own view of the merge-queue gate (erun#1931):
// what is being gated right now, and what recent gates decided -- reachable
// without knowing any job ids. Read-only: POST/PATCH stay internal, since the
// environment driving the gate reports its own attempt.
export function GateRunsPanel({ token }: { token: string }): React.ReactElement {
  const [status, setStatus] = React.useState('');
  const filter: GateRunFilter = status === '' ? {} : { status: status as GateRunStatus };
  const state = useGateRunsController(token, filter);

  return (
    <Card aria-labelledby="gate-runs-heading">
      <CardHeader>
        <CardTitle id="gate-runs-heading">
          <ListChecks className="mr-2 inline size-4" aria-hidden="true" />
          Gate runs
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4">
        <GateRunLookup token={token} />
        <div className="max-w-xs">
          <SelectField
            id="gate-run-status-filter"
            label="Status"
            value={status}
            options={STATUS_FILTER_OPTIONS}
            onChange={setStatus}
          />
        </div>
        <GateRunsBody state={state} />
      </CardContent>
    </Card>
  );
}
