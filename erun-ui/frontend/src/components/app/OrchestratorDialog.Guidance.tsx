import { Button, IconTooltip, Label } from 'erun-kit';
import { Blocks, Code2 } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import type { OrchestratorGuidanceLayer } from '@/app/model';
import { revealOrchestratorGuidance } from '@/app/orchestratorThunks';
import { ideLabel } from '@/app/terminalStatus';

import { OrchestratorGuidancePaths } from '../../../wailsjs/go/main/App';

interface GuidancePaths {
  role: string;
  shared: string;
}

// useOrchestratorGuidancePaths is read-only: opening the dialog never creates the workspace or role file.
function useOrchestratorGuidancePaths(orchestratorId: string): GuidancePaths | null {
  const [paths, setPaths] = React.useState<GuidancePaths | null>(null);

  React.useEffect(() => {
    let active = true;
    setPaths(null);
    void OrchestratorGuidancePaths(orchestratorId).then((resolved) => {
      if (active) {
        setPaths({ role: resolved.role, shared: resolved.shared });
      }
    });
    return () => {
      active = false;
    };
  }, [orchestratorId]);

  return paths;
}

// OrchestratorGuidanceSection opens both guidance layers in the operator's chosen IDE.
export function OrchestratorGuidanceSection({
  orchestratorId,
}: {
  orchestratorId: string;
}): React.ReactElement {
  const paths = useOrchestratorGuidancePaths(orchestratorId);
  return (
    <div className="space-y-1.5">
      <Label>Guidance</Label>
      <div className="space-y-2">
        <GuidanceRow
          orchestratorId={orchestratorId}
          layer="role"
          title="Role: what this orchestrator does"
          note="Yours to edit — erun creates it once and never overwrites it."
          path={paths?.role ?? ''}
        />
        <GuidanceRow
          orchestratorId={orchestratorId}
          layer="shared"
          title="Shared contract: rules for every orchestrator"
          note="erun-managed — rewritten on every launch, so edits here are lost."
          path={paths?.shared ?? ''}
        />
      </div>
    </div>
  );
}

function GuidanceRow({
  orchestratorId,
  layer,
  title,
  note,
  path,
}: {
  orchestratorId: string;
  layer: OrchestratorGuidanceLayer;
  title: string;
  note: string;
  path: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const reveal = (ide: 'vscode' | 'intellij'): void => {
    void dispatch(revealOrchestratorGuidance(orchestratorId, layer, ide));
  };
  return (
    <div className="rounded-sm border border-border/60 px-2 py-1.5">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-sm">{title}</p>
          <p className="text-xs text-muted-foreground">{note}</p>
          {path ? (
            <p className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{path}</p>
          ) : null}
        </div>
        <div className="flex flex-none gap-1">
          <IconTooltip label={`Open ${title} in ${ideLabel('vscode')}`}>
            <Button
              type="button"
              variant="outline"
              size="icon-xs"
              className="size-7"
              aria-label={`Open ${title} in ${ideLabel('vscode')}`}
              onClick={() => {
                reveal('vscode');
              }}
            >
              <Code2 aria-hidden="true" />
            </Button>
          </IconTooltip>
          <IconTooltip label={`Open ${title} in ${ideLabel('intellij')}`}>
            <Button
              type="button"
              variant="outline"
              size="icon-xs"
              className="size-7"
              aria-label={`Open ${title} in ${ideLabel('intellij')}`}
              onClick={() => {
                reveal('intellij');
              }}
            >
              <Blocks aria-hidden="true" />
            </Button>
          </IconTooltip>
        </div>
      </div>
    </div>
  );
}
