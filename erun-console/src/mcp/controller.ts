import * as React from 'react';

import { type MCPToken, mintMCPToken } from '../config/client';

// Thin controller for the MCP-access panel: per-environment, it mints a
// short-lived MCP bearer (POST /v1/environments/{id}/mcp-token) via the typed
// client and exposes the result. One-shot (no polling) — the operator mints a
// fresh token when they need one. State is keyed by environmentId so several
// envs are independent. No business logic beyond sequencing the call.

export type MCPTokenState =
  | { status: 'minting' }
  | { status: 'ready'; result: MCPToken }
  | { status: 'error'; message: string };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'unexpected error';
}

export interface MCPTokenController {
  // Per-environmentId mint state; an absent entry means no token minted this session.
  states: Record<string, MCPTokenState>;
  mint: (environmentId: string) => void;
}

export function useMCPTokenController(token: string): MCPTokenController {
  const [states, setStates] = React.useState<Record<string, MCPTokenState>>({});

  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const setEnvState = React.useCallback((environmentId: string, state: MCPTokenState) => {
    setStates((prev) => ({ ...prev, [environmentId]: state }));
  }, []);

  const mint = React.useCallback(
    (environmentId: string) => {
      setEnvState(environmentId, { status: 'minting' });
      mintMCPToken(token, environmentId)
        .then((result) => {
          if (activeRef.current) {
            setEnvState(environmentId, { status: 'ready', result });
          }
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setEnvState(environmentId, { status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, setEnvState],
  );

  return { states, mint };
}
