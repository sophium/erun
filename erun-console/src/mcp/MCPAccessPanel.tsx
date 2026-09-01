import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  type Environment,
  FieldLabel,
  Input,
  SelectField,
  Textarea,
} from 'erun-kit';
import { KeyRound } from 'lucide-react';
import * as React from 'react';

import type { McpTokenState, McpToolCallState } from './controller';
import { useMcpTokenController, useMcpToolCallController } from './controller';

// DriveToolForm is the console's first caller of an environment's live MCP
// edge, not just the token-minting half of it. The hostname is operator-
// supplied rather than looked up: exposing `mcp` is a manual `erun expose
// <tenant> <env> mcp` step today (not every environment type is exposed
// automatically), so the console has no reliable way to resolve it itself yet.
function DriveToolForm({ mcpToken }: { mcpToken: string }): React.ReactElement {
  const [hostname, setHostname] = React.useState('');
  const { state, callTool } = useMcpToolCallController();
  const calling = state.status === 'loading';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    if (hostname.trim() !== '') {
      callTool(hostname, mcpToken, 'version');
    }
  };

  return (
    <form
      className="grid gap-2 border-t border-border pt-3"
      onSubmit={submit}
      aria-labelledby="mcp-drive-heading"
    >
      <h3 id="mcp-drive-heading" className="text-sm font-semibold text-foreground">
        Drive this environment
      </h3>
      <FieldLabel htmlFor="mcp-hostname" required>
        MCP hostname (from erun expose &lt;tenant&gt; &lt;env&gt; mcp)
      </FieldLabel>
      <Input
        id="mcp-hostname"
        value={hostname}
        onChange={(e) => {
          setHostname(e.target.value);
        }}
        placeholder="mcp.acme-prod.services.example.com"
        required
      />
      <Button type="submit" disabled={calling} className="justify-self-start">
        {calling ? 'Calling…' : 'Call the version tool'}
      </Button>
      <DriveToolResult state={state} />
    </form>
  );
}

function DriveToolResult({ state }: { state: McpToolCallState }): React.ReactElement | null {
  if (state.status === 'ready') {
    return (
      <pre
        role="status"
        aria-live="polite"
        className={
          state.result.isError
            ? 'whitespace-pre-wrap text-sm text-destructive'
            : 'whitespace-pre-wrap text-sm text-muted-foreground'
        }
      >
        {state.result.text}
      </pre>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not call the tool: {state.message}
      </p>
    );
  }
  return null;
}

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
        <DriveToolForm mcpToken={state.token.token} />
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
        </form>
        <div className="mt-4 grid max-w-md gap-3">
          <TokenResult state={state} />
        </div>
      </CardContent>
    </Card>
  );
}
