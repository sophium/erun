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

// RequestActionState is keyed per inviteRequestId (see actionStates below),
// so approving or declining one row never blocks acting on another --
// mirrors environments/controller.ts's per-environmentId DeployState.
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

// useRequestsController lists pending invite requests and drives the
// per-row approve/decline actions. Approving surfaces the decided request
// (carrying the minted invite token) through `approved`, so the panel can
// show it once in a dialog even after the row itself disappears from the
// now-refetched pending list.
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

  // decline always resolves once the request settles, whether it succeeded
  // or failed -- mirroring identity/controller.ts's setActive -- so the
  // confirm dialog can await it to know when to stop showing its own
  // in-flight state; a failure surfaces through actionStates instead.
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
