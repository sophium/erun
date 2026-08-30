import { WhipNow, WhipTargets } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';
import { readError } from './errors';
import type { AppThunk } from './store';

export interface WhipOutcome {
  report: main.uiWhipReport | null;
  error: string | null;
}

// whipTargets fetches the current selectable population (every environment
// with a pod, every orchestrator live or configured) for the selection
// surface to render and "select all" to resolve against. A pure read, so the
// control can call it as often as it needs -- on open, on every "select all"
// click, and once more immediately before a click actually whips -- with no
// side effect (erun-ui/whip.go's WhipTargets).
export const whipTargets = (): AppThunk<Promise<main.uiWhipTargetList>> => async () =>
  WhipTargets();

// whipNow triggers one operator-initiated whip pass against the explicit
// target list the caller resolved (erun-ui/whip.go's WhipNow), returning the
// report -- or the call's own failure -- for the triggering control to render
// inline. A whip is a quick, non-destructive action, so its outcome belongs
// beside the button that started it rather than in a detached notification
// (erun-ui/AGENTS.md's Design-Language Decision Record, "Where a detached
// notification is earned").
export const whipNow =
  (targets: main.uiWhipTargetRef[]): AppThunk<Promise<WhipOutcome>> =>
  async () => {
    try {
      const report = await WhipNow(targets);
      return { report, error: null };
    } catch (error) {
      return { report: null, error: readError(error) };
    }
  };
