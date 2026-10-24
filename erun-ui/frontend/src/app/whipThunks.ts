import { WhipNow, WhipTargets } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';
import { readError } from './errors';
import { loadOrchestrators } from './orchestratorThunks';
import type { AppThunk } from './store';

export interface WhipOutcome {
  report: main.uiWhipReport | null;
  error: string | null;
}

export const whipTargets = (): AppThunk<Promise<main.uiWhipTargetList>> => async () =>
  WhipTargets();

// whipNow reloads orchestrators on success, since the hover card reads cached
// nudge history.
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
