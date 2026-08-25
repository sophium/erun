import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  type Environment,
  SelectField,
  Textarea,
} from 'erun-kit';
import { KeyRound } from 'lucide-react';
import * as React from 'react';

import type { McpTokenState } from './controller';
import { useMcpTokenController } from './controller';

function TokenResult({ state }: { state: McpTokenState }): React.ReactElement | null {
  if (state.status === 'ready') {
    return (
      <div className="grid gap-2 text-sm text-muted-foreground" role="status" aria-live="polite">
        <p>
          Bearer token for <code>{state.token.audience}</code>. Present it to the environment&apos;s
          MCP edge; it expires within the hour, so mint a fresh one when it lapses.
        </p>
        <Textarea
          className="font-mono text-xs"
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
      <p className="text-sm text-destructive" role="alert">
        Could not mint an MCP token: {state.message}
      </p>
    );
  }
  return null;
}

function EmptyPanel(): React.ReactElement {
  return (
    <Card aria-labelledby="mcp-access-heading">
      <CardHeader>
        <CardTitle id="mcp-access-heading">MCP access</CardTitle>
      </CardHeader>
      <CardContent>
        <EmptyState icon={<KeyRound />} heading="Register an environment to mint an MCP token." />
      </CardContent>
    </Card>
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
    <Card aria-labelledby="mcp-access-heading">
      <CardHeader>
        <CardTitle id="mcp-access-heading">MCP access</CardTitle>
      </CardHeader>
      <CardContent>
        <form className="grid max-w-md gap-3" onSubmit={submit}>
          <SelectField
            id="mcp-environment"
            label="Environment"
            value={selected}
            options={environments.map((env) => ({ value: env.environmentId, label: env.name }))}
            onChange={setSelected}
          />
          <Button
            type="submit"
            disabled={loading || selected === ''}
            className="justify-self-start"
          >
            {loading ? 'Minting…' : 'Generate MCP token'}
          </Button>
          <TokenResult state={state} />
        </form>
      </CardContent>
    </Card>
  );
}
