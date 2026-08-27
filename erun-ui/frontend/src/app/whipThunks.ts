import { WhipNow } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';
import { readError } from './errors';
import type { AppThunk } from './store';

export interface WhipOutcome {
  report: main.uiWhipReport | null;
  error: string | null;
}

// whipNow triggers one operator-initiated pass across every live orchestrator
// and configured environment (erun-ui/whip.go's WhipNow, shared with `erun
// whip`), returning the report -- or the call's own failure -- for the
// triggering control to render inline. A whip is a quick, non-destructive
// action, so its outcome belongs beside the button that started it rather
// than in a detached notification (erun-ui/AGENTS.md's Design-Language
// Decision Record, "Where a detached notification is earned").
export const whipNow = (): AppThunk<Promise<WhipOutcome>> => async () => {
  try {
    const report = await WhipNow();
    return { report, error: null };
  } catch (error) {
    return { report: null, error: readError(error) };
  }
};
