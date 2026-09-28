import type { AIActivityPayload } from './model';
import {
  setAIAwaitingForEnv,
  setAIBusyForEnv,
  setAIBusyForSession,
} from './slices/aiActivitySlice';
import type { AppThunk } from './store';
import { selectionKey } from './versionSuggestions';

// The two signals an env's AI tab can be in, as the ai-activity event reports
// them, surfaced while the user is looking at another env (Nielsen #1,
// visibility of system status).
//
// Both are applied from every payload, because the Go side emits them as one
// state: applying only the field that changed would leave a row spinning under
// a report that says the tool handed control back, and a "waiting on you"
// marker under one that says a turn just started. The policy that decides
// either value lives in erun-ui/terminal_sessions.go — this module only routes
// the event to the store.
export const handleAIActivity =
  (payload: AIActivityPayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      // An orchestrator session carries no env to key by. Dropping the event
      // here is why the orchestrator row never spun while it was working.
      if (payload.sessionId > 0) {
        dispatch(setAIBusyForSession({ sessionId: payload.sessionId, busy: payload.busy }));
      }
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(setAIBusyForEnv({ key, busy: payload.busy }));
    dispatch(setAIAwaitingForEnv({ key, awaiting: payload.awaitingInput === true }));
  };
