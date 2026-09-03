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

// MCP_ADMIN_SCOPE mirrors erun-common's `erun:admin` capability tier: every
// tool, including delete/terraform/init and the RCE-capable `raw`. The mint
// form offers it as an explicit escalation from the default MCP_OPERATE_SCOPE
// below -- full capability remains available for an operator who is handing
// a token to their own choice of MCP client and genuinely needs it (the case
// erun#1877 calls out as defensible), but requesting it also requires the
// delete-environment entitlement server-side, so it is no longer the mint
// form's default the way it was before `erun:operate` existed.
export const MCP_ADMIN_SCOPE = 'erun:admin';

// MCP_ATTACH_SCOPE mirrors erun-common's `erun:attach` capability tier: it
// drives the attach WebSocket's byte stream and nothing else, deliberately
// narrower than MCP_ADMIN_SCOPE -- a console session attaching to a live
// terminal has no reason to also hold `raw`/`build`/`deploy`/`delete`.
export const MCP_ATTACH_SCOPE = 'erun:attach';

// MCP_OPERATE_SCOPE mirrors erun-common's `erun:operate` capability tier
// (erun#1999/#2001): deploy/context_start/context_stop/resize on an
// environment that already exists, and nothing that decides what
// environments exist or runs arbitrary code in one. The mint form offers
// this as an alternative to MCP_ADMIN_SCOPE so an operator handing a token to
// a lower-trust caller -- a script, a teammate, a future mobile client --
// can choose the narrower blast radius when that is all the caller needs.
export const MCP_OPERATE_SCOPE = 'erun:operate';

export const mcpApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // requestMcpToken mints a per-env MCP bearer token the console presents
    // to the env's erun-mcp edge. The backend signs it, so the caller needs
    // no key. A 501 surfaces when the backend has no MCP signing key
    // configured. Every caller today passes its own scope explicitly (the
    // mint form's selector, the attach panel's fixed MCP_ATTACH_SCOPE); the
    // MCP_ADMIN_SCOPE fallback only guards a caller that omits it.
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
