import { Button } from 'erun-kit';
import { AlertTriangle, RotateCcw } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { restartOrchestrator } from '@/app/orchestratorThunks';
import { closeOrchestratorDialog, type OrchestratorInfo } from '@/app/slices/orchestratorsSlice';
import { restartNoticeText } from '@/components/app/OrchestratorDialog.RestartNotice.helpers';

// RestartRequiredNotice: a live session resolves its MCP tools at launch, so only a restart applies the edit.
// It carries the restart control and decides for itself whether to render.
export function RestartRequiredNotice({
  editing,
}: {
  editing: OrchestratorInfo | null;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((state) => state.orchestrators.busy);
  const text = editing?.status === 'running' ? restartNoticeText(editing) : null;
  if (!editing || !text) {
    return null;
  }
  const orchestratorId = editing.id;
  return (
    <div
      role="status"
      className="grid gap-1 rounded-[var(--radius)] border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-[12px] leading-[1.4] text-foreground"
    >
      <span className="flex items-start gap-2">
        <AlertTriangle aria-hidden="true" className="mt-[1px] size-3.5 shrink-0" />
        <span className="grid gap-1">
          <span>{text}</span>
          <span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => {
                dispatch(closeOrchestratorDialog());
                void dispatch(restartOrchestrator(orchestratorId));
              }}
            >
              <RotateCcw aria-hidden="true" />
              Restart now
            </Button>
          </span>
        </span>
      </span>
    </div>
  );
}
