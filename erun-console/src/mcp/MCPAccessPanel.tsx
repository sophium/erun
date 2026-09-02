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

import type { AttachSessionState, McpTokenState, McpToolCallState } from './controller';
import {
  useAttachSessionController,
  useMcpTokenController,
  useMcpToolCallController,
} from './controller';

// hostnameFieldLabel says whether a hostname field is prefilled from a known,
// discovered edge (still editable, to override) or falls back to plain
// manual entry because the environment has never been successfully exposed.
function hostnameFieldLabel(prefix: string, exposedHostname: string | undefined): string {
  return exposedHostname !== undefined
    ? `${prefix} (discovered — edit to override)`
    : `${prefix} (not yet exposed — enter one)`;
}

// DriveToolForm is the console's first caller of an environment's live MCP
// edge, not just the token-minting half of it. The hostname prefills from the
// environment's own ExposedHostname once the platform has actually exposed
// it; the field stays editable and falls back to plain manual entry for an
// environment that isn't exposed yet, or if the operator wants to point at a
// different hostname.
function DriveToolForm({
  mcpToken,
  exposedHostname,
}: {
  mcpToken: string;
  exposedHostname?: string;
}): React.ReactElement {
  const [hostname, setHostname] = React.useState(exposedHostname ?? '');
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
        {hostnameFieldLabel('MCP hostname', exposedHostname)}
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

// AttachSessionForm mints its own erun:attach-scoped token (never the
// erun:admin one DriveToolForm above uses) and opens a live WebSocket to an
// existing dtach session in the environment's pod -- the session id an
// operator already knows from `erun open --ai` or a linked orchestrator, not
// something this panel can discover on its own yet. This is a minimal,
// line-based view, not a full terminal: see useAttachSessionController's
// comment for why.
function AttachSessionForm({
  consoleToken,
  environmentId,
  exposedHostname,
}: {
  consoleToken: string;
  environmentId: string;
  exposedHostname?: string;
}): React.ReactElement {
  const [hostname, setHostname] = React.useState(exposedHostname ?? '');
  const [session, setSession] = React.useState('');
  const [line, setLine] = React.useState('');
  const { state, scrollback, connect, sendLine, disconnect } =
    useAttachSessionController(consoleToken);

  const busy = state.status === 'minting' || state.status === 'connecting';
  const connected = state.status === 'connected';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    if (hostname.trim() !== '' && session.trim() !== '') {
      connect(environmentId, hostname, session);
    }
  };

  const submitLine = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    if (line !== '') {
      sendLine(line);
      setLine('');
    }
  };

  return (
    <div className="grid gap-2 border-t border-border pt-3">
      <h3 className="text-sm font-semibold text-foreground">Attach to a live session</h3>
      <form className="grid gap-2" onSubmit={submit}>
        <FieldLabel htmlFor="attach-hostname" required>
          {hostnameFieldLabel('Environment edge hostname', exposedHostname)}
        </FieldLabel>
        <Input
          id="attach-hostname"
          value={hostname}
          onChange={(e) => {
            setHostname(e.target.value);
          }}
          placeholder="mcp.acme-prod.services.example.com"
          disabled={connected}
          required
        />
        <FieldLabel htmlFor="attach-session" required>
          Session id (from `erun open --ai` or a linked orchestrator)
        </FieldLabel>
        <Input
          id="attach-session"
          value={session}
          onChange={(e) => {
            setSession(e.target.value);
          }}
          disabled={connected}
          required
        />
        {connected ? (
          <Button
            type="button"
            variant="secondary"
            onClick={disconnect}
            className="justify-self-start"
          >
            Disconnect
          </Button>
        ) : (
          <Button type="submit" disabled={busy} className="justify-self-start">
            {busy ? 'Connecting…' : 'Attach'}
          </Button>
        )}
      </form>
      <AttachSessionStatus state={state} />
      {scrollback !== '' && (
        <pre
          role="log"
          aria-label="Attach session output"
          className="max-h-64 overflow-y-auto whitespace-pre-wrap rounded border border-border bg-muted p-2 font-mono text-xs text-foreground"
        >
          {scrollback}
        </pre>
      )}
      {connected && (
        <form className="flex gap-2" onSubmit={submitLine}>
          <Input
            aria-label="Send a line to the session"
            value={line}
            onChange={(e) => {
              setLine(e.target.value);
            }}
            placeholder="Type a line and press Enter"
          />
          <Button type="submit">Send</Button>
        </form>
      )}
    </div>
  );
}

function AttachSessionStatus({ state }: { state: AttachSessionState }): React.ReactElement | null {
  if (state.status === 'ended') {
    return (
      <p className="text-sm text-muted-foreground" role="status" aria-live="polite">
        Session ended: {state.outcome}
      </p>
    );
  }
  if (state.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not attach: {state.message}
      </p>
    );
  }
  return null;
}

function TokenResult({
  state,
  consoleToken,
  environmentId,
  exposedHostname,
}: {
  state: McpTokenState;
  consoleToken: string;
  environmentId: string;
  exposedHostname?: string;
}): React.ReactElement | null {
  if (state.status === 'ready') {
    return (
      <div
        key={environmentId}
        className="grid gap-2 text-sm text-muted-foreground"
        role="status"
        aria-live="polite"
      >
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
        <DriveToolForm mcpToken={state.token.token} exposedHostname={exposedHostname} />
        <AttachSessionForm
          consoleToken={consoleToken}
          environmentId={environmentId}
          exposedHostname={exposedHostname}
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

  const selectedEnvironment = environments.find((env) => env.environmentId === selected);

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
          <TokenResult
            state={state}
            consoleToken={token}
            environmentId={selected}
            exposedHostname={selectedEnvironment?.exposedHostname}
          />
        </div>
      </CardContent>
    </Card>
  );
}
