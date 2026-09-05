import { asOptionalString, asString, parseList } from 'erun-kit';

import { platformApi } from './platformApi';

// AISessionStatus mirrors eruncommon.AISessionStatus
// (erun-common/ai_session_status.go) — the busy/idle/awaiting-input status
// model resolved server-side from an environment's own AI-tool hook reports.
// Console-only for now (erun-kit/AGENTS.md moves a shape there only once a
// second transport needs it; the desktop has no reader for this yet).
export interface AISessionStatus {
  sessionId: string;
  tool?: string;
  state: string;
  reason: string;
  lastActivity?: string;
  exitCode?: number;
}

function parseAISessionStatus(raw: Record<string, unknown>): AISessionStatus {
  return {
    sessionId: asString(raw.sessionId),
    tool: asOptionalString(raw.tool),
    state: asString(raw.state),
    reason: asString(raw.reason),
    lastActivity: asOptionalString(raw.lastActivity),
    exitCode: typeof raw.exitCode === 'number' ? raw.exitCode : undefined,
  };
}

export const aiSessionsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // listAISessions reads back every session an environment's own AI-tool
    // hooks have reported — the read-only twin of the environment's own
    // POST self-report, for a caller with no local kubeconfig/port-forward.
    listAISessions: builder.query<AISessionStatus[], { token: string; environmentId: string }>({
      query: ({ token, environmentId }) => ({
        url: `/v1/environments/${encodeURIComponent(environmentId)}/ai-sessions`,
        token,
        label: 'AI sessions request',
      }),
      transformResponse: (raw: unknown) => parseList(raw, parseAISessionStatus),
      providesTags: (_result, _error, arg) => [{ type: 'Environment', id: arg.environmentId }],
    }),
  }),
});

export const { useListAISessionsQuery } = aiSessionsApi;
