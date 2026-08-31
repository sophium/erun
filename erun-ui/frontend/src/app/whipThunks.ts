import { WhipNow, WhipTargets } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';
import { readError } from './errors';
import { loadOrchestrators } from './orchestratorThunks';
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
//
// It also reloads the orchestrator list on success: an orchestrator's nudge
// history changed on the backend the moment the whip pushed, but the sidebar
// hover card reads from the store's own cached snapshot, not a live query --
// without this, the durable record a whip should have just updated stays
// stale until something else happens to refetch it.
export const whipNow =
  (targets: main.uiWhipTargetRef[]): AppThunk<Promise<WhipOutcome>> =>
  async (dispatch) => {
    try {
      const report = await WhipNow(targets);
      await dispatch(loadOrchestrators());
      return { report, error: null };
    } catch (error) {
      return { report: null, error: readError(error) };
    }
  };
