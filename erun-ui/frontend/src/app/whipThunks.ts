import { WhipNow, WhipTargets } from '../../wailsjs/go/main/App';
import type { orchestrator } from '../../wailsjs/go/models';
import { readError } from './errors';
import { loadOrchestrators } from './orchestratorThunks';
import type { AppThunk } from './store';

export interface WhipOutcome {
  report: orchestrator.WhipReport | null;
  error: string | null;
}

export const whipTargets = (): AppThunk<Promise<orchestrator.WhipTargetList>> => async () =>
  WhipTargets();

// whipNow reloads orchestrators on success, since the hover card reads cached
// nudge history.
export const whipNow =
  (targets: orchestrator.WhipTargetRef[]): AppThunk<Promise<WhipOutcome>> =>
  async (dispatch) => {
    try {
      const report = await WhipNow(targets);
      await dispatch(loadOrchestrators());
      return { report, error: null };
    } catch (error) {
      return { report: null, error: readError(error) };
    }
  };
