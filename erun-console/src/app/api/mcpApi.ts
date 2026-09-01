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
const MCP_ADMIN_SCOPE = 'erun:admin';

export const mcpApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // requestMcpToken mints a per-env MCP bearer token the console presents
    // to the env's erun-mcp edge. The backend signs it, so the caller needs
    // no key. A 501 surfaces when the backend has no MCP signing key
    // configured.
    requestMcpToken: builder.mutation<McpToken, { token: string; environmentId: string }>({
      query: ({ token, environmentId }) => ({
        url: `/v1/environments/${encodeURIComponent(environmentId)}/mcp-token`,
        method: 'POST',
        body: { scope: MCP_ADMIN_SCOPE },
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
