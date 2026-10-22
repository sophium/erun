import { Button } from 'erun-kit';
import { AlertTriangle, RotateCcw } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { restartOrchestrator } from '@/app/orchestratorThunks';
import { closeOrchestratorDialog, type OrchestratorInfo } from '@/app/slices/orchestratorsSlice';
import { restartNoticeText } from '@/components/app/OrchestratorDialog.RestartNotice.helpers';

// RestartRequiredNotice tells the operator, at the top of the form they just
// opened, that their earlier edit did not take effect yet: a live Claude Code
// session resolves its MCP toolset once at launch, so it still holds tools for
// whatever it was linked to before and none for what was added since — only a
// restart re-wires it. It carries the exact control that resolves
// it (root AGENTS.md: "a message naming a remedy should carry the action that
// resolves it") rather than pointing at the footer's Restart button below.
// Takes the raw `editing` value and decides for itself whether there is
// anything to say, so the caller stays a plain, unconditional render.
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
