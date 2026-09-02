import { asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

// The per-env MCP bearer token the backend mints for the console, plus the
// audience it was minted for (`erun-mcp:<tenant>/<env>`) and the capability
// scope it actually carries.
export interface McpToken {
  token: string;
  audience: string;
  scope: string;
}

// MCP_ADMIN_SCOPE mirrors erun-common's `erun:admin` capability tier. The
// console mints admin explicitly rather than relying on the endpoint's
// default: the operator is handing this token to their own choice of MCP
// client for their own environment, the one case erun#1877 calls out as
// defensible to keep at full capability -- an unspecified request now mints
// the safer `erun:read` instead.
export const MCP_ADMIN_SCOPE = 'erun:admin';

// MCP_ATTACH_SCOPE mirrors erun-common's `erun:attach` capability tier: it
// drives the attach WebSocket's byte stream and nothing else, deliberately
// narrower than MCP_ADMIN_SCOPE -- a console session attaching to a live
// terminal has no reason to also hold `raw`/`build`/`deploy`/`delete`.
export const MCP_ATTACH_SCOPE = 'erun:attach';

export const mcpApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // requestMcpToken mints a per-env MCP bearer token the console presents
    // to the env's erun-mcp edge. The backend signs it, so the caller needs
    // no key. A 501 surfaces when the backend has no MCP signing key
    // configured. scope defaults to MCP_ADMIN_SCOPE, the console's existing
    // "drive this environment" behaviour; a narrower caller (e.g. the attach
    // panel) passes its own scope explicitly.
    requestMcpToken: builder.mutation<
      McpToken,
      { token: string; environmentId: string; scope?: string }
    >({
      query: ({ token, environmentId, scope }) => ({
        url: `/v1/environments/${encodeURIComponent(environmentId)}/mcp-token`,
        method: 'POST',
        body: { scope: scope ?? MCP_ADMIN_SCOPE },
        token,
        label: 'mcp token request',
      }),
      transformResponse: (raw: unknown) => {
        if (!isRecord(raw)) {
          throw new Error('mcp token response was not in the expected shape');
        }
        return {
          token: asString(raw.token),
          audience: asString(raw.audience),
          scope: asString(raw.scope),
        };
      },
    }),
  }),
});

export const { useRequestMcpTokenMutation } = mcpApi;
