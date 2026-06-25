import * as React from 'react';

import type { Environment } from '../config/types';
import type { MCPTokenState } from './controller';
import { useMCPTokenController } from './controller';

// "Connect to an environment's MCP" panel: for each DEPLOYED runtime env it mints
// a short-lived MCP access token (POST /v1/environments/{id}/mcp-token) the
// operator presents as a Bearer token to that env's MCP edge. Only deployed envs
// are shown — the endpoint returns 409 until an env's MCP edge exists. Thin
// render layer over the controller; no business logic here.

function statusLine(state: MCPTokenState | undefined): string {
  switch (state?.status) {
    case undefined:
      return '';
    case 'minting':
      return 'Minting access token…';
    case 'ready':
      return `Token scoped to ${state.result.audience}. Present it as a Bearer token to the environment's MCP edge.`;
    case 'error':
      return `Request failed: ${state.message}`;
  }
}

function MCPAccessRow({
  environment,
  state,
  onMint,
}: {
  environment: Environment;
  state: MCPTokenState | undefined;
  onMint: (environmentId: string) => void;
}): React.ReactElement {
  const busy = state?.status === 'minting';
  const line = statusLine(state);
  return (
    <li className="deploy-row">
      <span className="deploy-env-name">{environment.name}</span>
      <button
        type="button"
        disabled={busy}
        onClick={() => {
          onMint(environment.environmentId);
        }}
      >
        {busy ? 'Minting…' : 'Get MCP access token'}
      </button>
      {state?.status === 'ready' && (
        <textarea
          className="mcp-token"
          readOnly
          value={state.result.token}
          aria-label={`MCP access token for ${environment.name}`}
        />
      )}
      {line !== '' && (
        <div
          className="deploy-status"
          role={state?.status === 'error' ? 'alert' : 'status'}
          aria-live="polite"
        >
          <p>{line}</p>
        </div>
      )}
    </li>
  );
}

export function MCPAccessPanel({
  token,
  environments,
}: {
  token: string;
  environments: Environment[];
}): React.ReactElement {
  const { states, mint } = useMCPTokenController(token);
  const deployed = environments.filter(
    (environment) => environment.type === 'runtime' && environment.deployStatus === 'deployed',
  );
  return (
    <section className="mcp-panel" aria-labelledby="mcp-heading">
      <h2 id="mcp-heading">Connect to an environment&apos;s MCP</h2>
      {deployed.length === 0 ? (
        <p className="empty-state">No deployed environments to connect to.</p>
      ) : (
        <ul className="deploy-list">
          {deployed.map((environment) => (
            <MCPAccessRow
              key={environment.environmentId}
              environment={environment}
              state={states[environment.environmentId]}
              onMint={mint}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
