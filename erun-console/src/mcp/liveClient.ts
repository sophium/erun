import { asString, isRecord } from 'erun-kit';

// liveClient speaks the MCP edge's own wire protocol directly from the
// browser: JSON-RPC 2.0 over POST, an `initialize` handshake that returns a
// session id in the `Mcp-Session-Id` response header, a `notifications/
// initialized` confirmation carrying that header, then `tools/call` — the
// same sequence `erun-mcp/AGENTS.md` documents for a raw-HTTP probe. Each
// call opens a fresh session rather than caching one across calls: this is
// the console's first caller of the live edge, and a cached session adds
// reconnect/expiry handling this increment does not need yet.

const PROTOCOL_VERSION = '2025-06-18';
const SESSION_HEADER = 'Mcp-Session-Id';

export interface McpToolResult {
  isError: boolean;
  text: string;
}

// mcpEdgeUrl builds the JSON-RPC endpoint from the hostname an operator
// copies out of `erun expose <tenant> <env> mcp`'s own output (a bare host,
// no scheme or path) -- tolerant of a pasted scheme or trailing path too, so
// a caller who pastes what they see elsewhere (e.g. a browser address bar)
// is not refused for it.
export function mcpEdgeUrl(hostname: string): string {
  const trimmed = hostname.trim().replace(/\/+$/, '');
  const withScheme = /^https?:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
  return withScheme.endsWith('/mcp') ? withScheme : `${withScheme}/mcp`;
}

interface PostMcpParams {
  url: string;
  token: string;
  sessionId?: string;
  body: Record<string, unknown>;
}

async function postMcp({ url, token, sessionId, body }: PostMcpParams): Promise<Response> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'application/json, text/event-stream',
    Authorization: `Bearer ${token}`,
  };
  if (sessionId !== undefined) {
    headers[SESSION_HEADER] = sessionId;
  }
  try {
    return await fetch(url, { method: 'POST', headers, body: JSON.stringify(body) });
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new Error(`could not reach ${url}: ${detail}`);
  }
}

async function readJsonRpcResult(response: Response, url: string): Promise<unknown> {
  if (!response.ok) {
    const detail = await response.text().catch(() => '');
    throw new Error(
      `${url} responded ${String(response.status)}${detail !== '' ? `: ${detail}` : ''}`,
    );
  }
  const body: unknown = await response.json();
  if (!isRecord(body)) {
    throw new Error(`${url} returned an unexpected response shape`);
  }
  if (isRecord(body.error)) {
    throw new Error(asString(body.error.message) || `${url} rejected the call`);
  }
  return body.result;
}

async function initializeSession(url: string, token: string): Promise<string> {
  const response = await postMcp({
    url,
    token,
    body: {
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: { name: 'erun-console', version: '0' },
      },
    },
  });
  await readJsonRpcResult(response, url);
  const sessionId = response.headers.get(SESSION_HEADER);
  if (sessionId === null || sessionId === '') {
    throw new Error(`${url} did not return a ${SESSION_HEADER} header`);
  }
  return sessionId;
}

async function confirmInitialized(url: string, token: string, sessionId: string): Promise<void> {
  const response = await postMcp({
    url,
    token,
    sessionId,
    body: { jsonrpc: '2.0', method: 'notifications/initialized' },
  });
  if (!response.ok) {
    throw new Error(`${url} rejected notifications/initialized (${String(response.status)})`);
  }
}

async function requestToolCall(
  url: string,
  token: string,
  sessionId: string,
  toolName: string,
  args: Record<string, unknown>,
): Promise<unknown> {
  const response = await postMcp({
    url,
    token,
    sessionId,
    body: {
      jsonrpc: '2.0',
      id: 2,
      method: 'tools/call',
      params: { name: toolName, arguments: args },
    },
  });
  return readJsonRpcResult(response, url);
}

function toolResultText(result: Record<string, unknown>): string {
  const content = Array.isArray(result.content) ? result.content : [];
  const text = content
    .filter(isRecord)
    .map((item) => asString(item.text))
    .filter((value) => value !== '')
    .join('\n');
  if (text !== '') {
    return text;
  }
  if (result.structuredContent !== undefined) {
    return JSON.stringify(result.structuredContent, null, 2);
  }
  return JSON.stringify(result, null, 2);
}

function parseToolResult(result: unknown): McpToolResult {
  if (!isRecord(result)) {
    return { isError: false, text: JSON.stringify(result) };
  }
  return { isError: result.isError === true, text: toolResultText(result) };
}

// callMcpTool drives one tool call against a deployed environment's exposed
// MCP edge, from the browser, using a bearer token the console already
// minted (see mcpApi.ts). It is the one function that actually exercises
// that token; everything upstream only requests and displays it. args
// defaults to an empty object for the read-only smoke-test caller
// (DriveToolForm's `version` call), which takes no input.
export async function callMcpTool(
  hostname: string,
  token: string,
  toolName: string,
  args: Record<string, unknown> = {},
): Promise<McpToolResult> {
  const url = mcpEdgeUrl(hostname);
  const sessionId = await initializeSession(url, token);
  await confirmInitialized(url, token, sessionId);
  return parseToolResult(await requestToolCall(url, token, sessionId, toolName, args));
}
