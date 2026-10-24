import { asOptionalString, asString, parseList } from 'erun-kit';

import { platformApi } from './platformApi';

// AISessionStatus mirrors eruncommon.AISessionStatus (erun-common/ai_session_status.go).
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
