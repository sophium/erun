import { IconTooltip } from 'erun-kit';
import { CircleHelp, PowerOff, Server } from 'lucide-react';
import * as React from 'react';

import type { CloudNodeState } from '@/app/cloudNodeStatus';
import type { EnvironmentNodeIndicator } from '@/app/environmentNodeState';

// Sidebar.NodeStateIndicator.tsx is the environment row's cloud-node indicator:
// whether the machine the environment's cluster runs on is up. It is a FOURTH
// row-kind glyph, deliberately not built on Sidebar.StatusDot.tsx's
// StatusDotGlyph — that component's own doc comment scopes it to the
// env/orchestrator condition vocabulary (running / busy / stopped / failed), and
// a stopped NODE and a stopped ENVIRONMENT are different facts about different
// objects with different remedies. Folding the node into that union would let a
// future env-only change silently alter this glyph, and would lose exactly the
// distinction this indicator exists to add. Same reasoning
// Sidebar.TenantEnrollmentStatus.tsx applied for platform enrollment.
//
// Shape carries the state, not colour alone (WCAG 1.4.1): a crossed-out power
// symbol = stopped, a server outline = starting, a question mark = state
// unknown. It is a passive status light, never a control: starting a node is a
// real cloud operation, and the titlebar's own power control owns it — the
// label names that route rather than growing a second, unconfirmed one here.

function NodeGlyph({ state }: { state: CloudNodeState }): React.ReactElement | null {
  if (state === 'stopped') {
    return <PowerOff aria-hidden="true" className="size-2.5 text-muted-foreground" />;
  }
  if (state === 'pending') {
    return <Server aria-hidden="true" className="size-2.5 text-emerald-600" />;
  }
  if (state === 'unknown') {
    return <CircleHelp aria-hidden="true" className="size-2.5 text-muted-foreground" />;
  }
  return null;
}

export function NodeStateIndicator({
  indicator,
}: {
  indicator: EnvironmentNodeIndicator;
}): React.ReactElement | null {
  if (!indicator.visible || indicator.state === 'running') {
    return null;
  }
  return (
    <IconTooltip label={indicator.condition}>
      <span
        role="img"
        aria-label={indicator.condition}
        data-testid="env-node-indicator"
        data-node-state={indicator.state}
        className="flex size-[18px] flex-none items-center justify-center rounded-full text-current"
      >
        <NodeGlyph state={indicator.state} />
      </span>
    </IconTooltip>
  );
}
