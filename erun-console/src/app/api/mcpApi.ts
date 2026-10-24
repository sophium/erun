import { asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

// Audience is `erun-mcp:<tenant>/<env>`.
export interface McpToken {
  token: string;
  audience: string;
  scope: string;
}

// MCP_ADMIN_SCOPE grants every tool including `raw`; requesting it also needs the
// delete-environment entitlement server-side, so it is not the default.
export const MCP_ADMIN_SCOPE = 'erun:admin';

// MCP_ATTACH_SCOPE drives only the attach WebSocket's byte stream.
export const MCP_ATTACH_SCOPE = 'erun:attach';

// MCP_OPERATE_SCOPE operates existing environments without creating them or running arbitrary code.
export const MCP_OPERATE_SCOPE = 'erun:operate';

export const mcpApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // A 501 means the backend has no MCP signing key configured.
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
