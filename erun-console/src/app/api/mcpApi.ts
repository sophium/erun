import { asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

// The per-env MCP bearer token the backend mints for the console, plus the
// audience it was minted for (`erun-mcp:<tenant>/<env>`).
export interface McpToken {
  token: string;
  audience: string;
}

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
        token,
        label: 'mcp token request',
      }),
      transformResponse: (raw: unknown) => {
        if (!isRecord(raw)) {
          throw new Error('mcp token response was not in the expected shape');
        }
        return { token: asString(raw.token), audience: asString(raw.audience) };
      },
    }),
  }),
});

export const { useRequestMcpTokenMutation } = mcpApi;
