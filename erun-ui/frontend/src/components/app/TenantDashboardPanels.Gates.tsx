import { EmptyState, StatusBadge, TabsContent } from 'erun-kit';
import * as React from 'react';

import { gateRunStatusTone } from '@/app/tenantDashboardPanels';
import type { UIGateRun } from '@/types';

import type { TenantDashboardData } from './TenantDashboardMessage';
import {
  BranchArrow,
  DataCell,
  DataTable,
  PanelBody,
  RelativeTime,
} from './TenantDashboardMessage';

// GatesPanel answers, without the operator knowing any job ids: what is
// being gated right now, and what recent gates decided (erun#1932). It is
// the desktop counterpart to `erun gate list`/`erun gate show`.
//
// The one thing this must get right: INCONCLUSIVE is not a failure --
// gateRunStatusTone renders it with its own `warning` tone, visibly distinct
// from FAILED's `destructive` tone (see erun-backend-api/AGENTS.md's "Gate
// Runs").
export function GatesPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const gateRuns = data?.gateRuns ?? [];
  return (
    <TabsContent value="gates" className="min-h-0 overflow-auto">
      <PanelBody
        data={data}
        tab="gates"
        empty={
          <EmptyState
            heading="No gate runs yet"
            body="A gate run appears here the moment an environment starts gating a prospective merge."
          />
        }
      >
        {gateRuns.length > 0 ? <GateRunsTable gateRuns={gateRuns} /> : null}
      </PanelBody>
    </TabsContent>
  );
}

function shortCommit(commit: string | undefined): string {
  if (commit === undefined || commit === '') {
    return '-';
  }
  return commit.slice(0, 8);
}

function GateRunOutcome({ run }: { run: UIGateRun }): React.ReactElement {
  if (run.status !== 'FAILED') {
    return <></>;
  }
  return (
    <div className="grid gap-0.5">
      <span className="text-foreground">{run.failingStep ?? 'unknown step'}</span>
      {run.logRef !== undefined && (
        <span className="truncate text-xs text-muted-foreground" title={run.logRef}>
          {run.logRef}
        </span>
      )}
    </div>
  );
}

function GateRunsTable({ gateRuns }: { gateRuns: UIGateRun[] }): React.ReactElement {
  return (
    <DataTable
      headers={['Status', 'Branch', 'Merge commit', 'Review', 'Outcome', 'Updated']}
      columnWidths={['w-[130px]', '', 'w-[110px]', '', '', 'w-[120px]']}
      minWidthClassName="min-w-[900px]"
    >
      {gateRuns.map((run) => (
        <tr key={run.gateRunId}>
          <DataCell>
            <StatusBadge tone={gateRunStatusTone(run.status)} label={run.status} />
          </DataCell>
          <DataCell>
            <BranchArrow source={run.sourceBranch} target={run.targetBranch} />
          </DataCell>
          <DataCell>
            <span className="font-mono text-xs">{shortCommit(run.mergeCommit)}</span>
          </DataCell>
          <DataCell>{run.reviewName ?? run.reviewId}</DataCell>
          <DataCell>
            <GateRunOutcome run={run} />
          </DataCell>
          <DataCell>
            <RelativeTime value={run.updatedAt} />
          </DataCell>
        </tr>
      ))}
    </DataTable>
  );
}
