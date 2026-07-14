import * as React from 'react';

import type { Environment } from '../config/types';
import type { McpTokenState } from './controller';
import { useMcpTokenController } from './controller';

function TokenResult({ state }: { state: McpTokenState }): React.ReactElement | null {
  if (state.status === 'ready') {
    return (
      <div className="mcp-token-result" role="status" aria-live="polite">
        <p>
          Bearer token for <code>{state.token.audience}</code>. Present it to the environment&apos;s
          MCP edge; it expires within the hour, so mint a fresh one when it lapses.
        </p>
        <textarea
          className="mcp-token-value"
          aria-label="MCP bearer token"
          value={state.token.token}
          rows={4}
          readOnly
        />
      </div>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="mcp-token-error" role="alert">
        Could not mint an MCP token: {state.message}
      </p>
    );
  }
  return null;
}

function EmptyPanel(): React.ReactElement {
  return (
    <section className="mcp-access-panel" aria-labelledby="mcp-access-heading">
      <h2 id="mcp-access-heading">MCP access</h2>
      <p className="mcp-empty">Register an environment to mint an MCP token.</p>
    </section>
  );
}

// MCPAccessPanel mints a per-env MCP bearer token and surfaces it for the
// operator to hand to an MCP client. The backend signs the token, so the
// browser needs no signing key. Driving tools over the live edge is separate.
export function MCPAccessPanel({
  token,
  environments,
}: {
  token: string;
  environments: Environment[];
}): React.ReactElement {
  const { state, requestToken } = useMcpTokenController(token);
  const [selected, setSelected] = React.useState(environments[0]?.environmentId ?? '');

  if (environments.length === 0) {
    return <EmptyPanel />;
  }

  const loading = state.status === 'loading';
  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    if (selected !== '') {
      requestToken(selected);
    }
  };

  return (
    <section className="mcp-access-panel" aria-labelledby="mcp-access-heading">
      <h2 id="mcp-access-heading">MCP access</h2>
      <form className="mcp-access-form" onSubmit={submit}>
        <label htmlFor="mcp-environment">Environment</label>
        <select
          id="mcp-environment"
          value={selected}
          onChange={(e) => {
            setSelected(e.target.value);
          }}
        >
          {environments.map((env) => (
            <option key={env.environmentId} value={env.environmentId}>
              {env.name}
            </option>
          ))}
        </select>
        <button type="submit" disabled={loading || selected === ''}>
          {loading ? 'Minting…' : 'Generate MCP token'}
        </button>
        <TokenResult state={state} />
      </form>
    </section>
  );
}
