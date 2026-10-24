import { IconTooltip } from 'erun-kit';
import { CircleHelp, PowerOff, Server } from 'lucide-react';
import * as React from 'react';

import type { CloudNodeState } from '@/app/cloudNodeStatus';
import type { EnvironmentNodeIndicator } from '@/app/environmentNodeState';

// Cloud-node indicator for an environment row. Deliberately not StatusDotGlyph:
// a stopped node and a stopped environment are different facts with different
// remedies. Passive only; the titlebar's power control starts the node.

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
