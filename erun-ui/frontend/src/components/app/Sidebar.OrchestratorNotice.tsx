import { HelpCircle, Info } from 'lucide-react';
import * as React from 'react';

import type { OrchestratorNotice } from '@/app/orchestratorRestore';
import { InlineAlert } from '@/components/app/InlineAlert';

// OrchestratorNoticeList renders every restore notice at its own severity,
// one line each, instead of the single destructive paragraph they used to be
// joined into — a warning among several routine successes must stay visually
// distinct from them, not disappear into the same red block.
export function OrchestratorNoticeList({
  notices,
}: {
  notices: OrchestratorNotice[];
}): React.ReactElement | null {
  if (notices.length === 0) {
    return null;
  }
  return (
    <ul className="flex flex-col gap-1 px-3.5 pb-1" aria-label="Orchestrator restore notices">
      {notices.map((notice, index) => (
        <li key={`${notice.orchestratorId ?? 'restore'}-${notice.kind}-${String(index)}`}>
          <OrchestratorNoticeRow notice={notice} />
        </li>
      ))}
    </ul>
  );
}

// warning keeps the same destructive alert treatment InlineAlert already gives
// a write that was attempted and refused — these notices report exactly that
// kind of failed resolution.
function OrchestratorNoticeRow({ notice }: { notice: OrchestratorNotice }): React.ReactElement {
  if (notice.kind === 'warning') {
    return <InlineAlert>{notice.text}</InlineAlert>;
  }
  if (notice.kind === 'info') {
    // The mechanism working correctly is information, not a fault: role="status"
    // announces politely instead of interrupting the way role="alert" does, and
    // the layout matches InlineAlert's without borrowing its destructive colour.
    return (
      <div
        role="status"
        className="flex w-full items-start gap-2 rounded-[var(--radius)] border border-border bg-muted/40 px-[11px] py-[9px] text-[13px] leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]"
      >
        <Info className="mt-px size-3.5 shrink-0" aria-hidden="true" />
        <span className="min-w-0">{notice.text}</span>
      </div>
    );
  }
  // notice.kind === 'unknown': a launch that could not classify this notice's
  // severity. Defaulting it to info would hide a real problem behind a
  // routine-looking line; defaulting it to warning would cry wolf over what
  // might be entirely routine. role="status" keeps it from interrupting like
  // an alert while the distinct icon and dashed border make clear this is
  // neither of the two known kinds.
  return (
    <div
      role="status"
      className="flex w-full items-start gap-2 rounded-[var(--radius)] border border-dashed border-border px-[11px] py-[9px] text-[13px] leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]"
    >
      <HelpCircle className="mt-px size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0">{notice.text}</span>
    </div>
  );
}
