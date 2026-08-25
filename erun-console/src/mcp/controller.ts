import * as React from 'react';

import type { McpToken } from '../app/api/mcpApi';
import { useRequestMcpTokenMutation } from '../app/api/mcpApi';
import { queryErrorMessage } from '../app/queryError';

export type McpTokenState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; token: McpToken }
  | { status: 'error'; message: string };

export interface McpTokenController {
  state: McpTokenState;
  requestToken: (environmentId: string) => void;
}

// useMcpTokenController mints a per-env MCP token on demand. A single request,
// no polling; the active-ref guard drops a response that lands after unmount.
export function useMcpTokenController(token: string): McpTokenController {
  const [state, setState] = React.useState<McpTokenState>({ status: 'idle' });
  const [requestMcpToken] = useRequestMcpTokenMutation();

  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const requestToken = React.useCallback(
    (environmentId: string) => {
      setState({ status: 'loading' });
      requestMcpToken({ token, environmentId })
        .unwrap()
        .then((minted) => {
          if (activeRef.current) {
            setState({ status: 'ready', token: minted });
          }
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setState({ status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, requestMcpToken],
  );

  return { state, requestToken };
}
