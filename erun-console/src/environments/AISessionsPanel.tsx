import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  type Environment,
  StatusBadge,
  type StatusBadgeTone,
} from 'erun-kit';
import * as React from 'react';

import { type AISessionStatus, useListAISessionsQuery } from '../app/api/aiSessionsApi';
import { useListEnvironmentsQuery } from '../app/api/environmentsApi';

// Read-only AI session status, fetched on demand for one expanded environment since
// most never report a session.

const STATE_TONE: Record<string, StatusBadgeTone> = {
  busy: 'in-progress',
  'awaiting-input': 'warning',
  idle: 'muted',
  exited: 'muted',
  'oom-killed': 'destructive',
};

function stateTone(state: string): StatusBadgeTone {
  return STATE_TONE[state] ?? 'muted';
}

function SessionRow({ session }: { session: AISessionStatus }): React.ReactElement {
  return (
    <li className="grid gap-1 border-b border-border py-2 last:border-b-0">
      <div className="flex items-center gap-2">
        <span className="font-mono text-sm text-foreground">{session.sessionId}</span>
        {session.tool !== undefined && (
          <StatusBadge tone="muted" label={session.tool} showIcon={false} />
        )}
        <StatusBadge tone={stateTone(session.state)} label={session.state} />
      </div>
      <p className="text-xs text-muted-foreground">{session.reason}</p>
    </li>
  );
}

function AISessionsList({
  token,
  environmentId,
}: {
  token: string;
  environmentId: string;
}): React.ReactElement {
  const { data, isLoading, error } = useListAISessionsQuery({ token, environmentId });

  if (isLoading) {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading AI sessions…
      </p>
    );
  }
  if (error !== undefined) {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load AI sessions.
      </p>
    );
  }
  if (data === undefined || data.length === 0) {
    return <EmptyState heading="No AI sessions reported yet" />;
  }
  return (
    <ul>
      {data.map((session) => (
        <SessionRow key={session.sessionId} session={session} />
      ))}
    </ul>
  );
}

function AISessionsRow({
  token,
  environment,
}: {
  token: string;
  environment: Environment;
}): React.ReactElement {
  const [expanded, setExpanded] = React.useState(false);
  return (
    <li className="border-b border-border py-3 last:border-b-0">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-foreground">{environment.name}</span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            setExpanded((current) => !current);
          }}
        >
          {expanded ? 'Hide AI sessions' : 'Show AI sessions'}
        </Button>
      </div>
      {expanded && (
        <div className="mt-2">
          <AISessionsList token={token} environmentId={environment.environmentId} />
        </div>
      )}
    </li>
  );
}

export function AISessionsPanel({
  token,
  environments,
  scopeTenantId,
}: {
  token: string;
  environments: Environment[];
  // Undefined means the caller's own tenant.
  scopeTenantId?: string;
}): React.ReactElement {
  const scopedEnvironmentsQuery = useListEnvironmentsQuery(
    { token, tenantId: scopeTenantId },
    { skip: scopeTenantId === undefined },
  );
  const visibleEnvironments =
    scopeTenantId !== undefined ? (scopedEnvironmentsQuery.data ?? []) : environments;
  return (
    <Card aria-labelledby="ai-sessions-panel-heading">
      <CardHeader>
        <CardTitle id="ai-sessions-panel-heading">AI sessions</CardTitle>
      </CardHeader>
      <CardContent>
        {visibleEnvironments.length === 0 ? (
          <EmptyState heading="No environments to show AI sessions for" />
        ) : (
          <ul>
            {visibleEnvironments.map((environment) => (
              <AISessionsRow
                key={environment.environmentId}
                token={token}
                environment={environment}
              />
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
