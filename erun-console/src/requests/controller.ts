import * as React from 'react';

import type { InviteRequest } from '../app/api/requestsApi';
import {
  PENDING_REQUESTS_POLL_MS,
  useApproveInviteRequestMutation,
  useDeclineInviteRequestMutation,
  useListInviteRequestsQuery,
} from '../app/api/requestsApi';
import { queryErrorMessage } from '../app/queryError';

export type RequestsState =
  | { status: 'loading' }
  | { status: 'ready'; requests: InviteRequest[] }
  | { status: 'error'; message: string };

// Keyed per inviteRequestId so one row's action never blocks another.
export type RequestActionState =
  | { status: 'idle' }
  | { status: 'busy' }
  | { status: 'error'; message: string };

export interface RequestsController {
  requestsState: RequestsState;
  actionStates: Record<string, RequestActionState>;
  approve: (id: string) => void;
  decline: (id: string, reason: string) => Promise<void>;
  approved: InviteRequest | undefined;
  dismissApproved: () => void;
}

function useActiveRef(): React.RefObject<boolean> {
  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);
  return activeRef;
}

// `approved` outlives the row, which disappears from the refetched pending list.
export function useRequestsController(token: string): RequestsController {
  const listQuery = useListInviteRequestsQuery(token, {
    pollingInterval: PENDING_REQUESTS_POLL_MS,
  });
  const [actionStates, setActionStates] = React.useState<Record<string, RequestActionState>>({});
  const [approved, setApproved] = React.useState<InviteRequest | undefined>(undefined);
  const [approveInviteRequest] = useApproveInviteRequestMutation();
  const [declineInviteRequest] = useDeclineInviteRequestMutation();
  const activeRef = useActiveRef();

  const requestsState: RequestsState =
    listQuery.error !== undefined
      ? { status: 'error', message: queryErrorMessage(listQuery.error) }
      : listQuery.data !== undefined
        ? { status: 'ready', requests: listQuery.data }
        : { status: 'loading' };

  const setActionState = React.useCallback((id: string, state: RequestActionState) => {
    setActionStates((prev) => ({ ...prev, [id]: state }));
  }, []);

  const approve = React.useCallback(
    (id: string) => {
      setActionState(id, { status: 'busy' });
      approveInviteRequest({ token, id })
        .unwrap()
        .then((decided) => {
          if (!activeRef.current) {
            return;
          }
          setActionState(id, { status: 'idle' });
          setApproved(decided);
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setActionState(id, { status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, approveInviteRequest, setActionState, activeRef],
  );

  // Resolves on success or failure so the confirm dialog can await it.
  const decline = React.useCallback(
    (id: string, reason: string): Promise<void> => {
      setActionState(id, { status: 'busy' });
      return declineInviteRequest({ token, id, reason })
        .unwrap()
        .then(() => {
          if (activeRef.current) {
            setActionState(id, { status: 'idle' });
          }
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setActionState(id, { status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, declineInviteRequest, setActionState, activeRef],
  );

  const dismissApproved = React.useCallback(() => {
    setApproved(undefined);
  }, []);

  return { requestsState, actionStates, approve, decline, approved, dismissApproved };
}
