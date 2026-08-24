import { type CloudContext, isRecord, parseCloudContext } from 'erun-kit';

import { type NoValue, platformApi } from './platformApi';

// The BYO-cloud credentials an operator registers under an alias. `provider`
// defaults to aws server-side; `credentials` is an opaque provider-specific
// JSON string the API encrypts at rest (never returned).
export interface CloudProviderAliasInput {
  provider?: string;
  credentials: string;
}

// The fields needed to register (provision) a cloud context.
export interface CreateContextInput {
  name: string;
  cloudProviderAlias: string;
  region: string;
}

function parseCloudContextResponse(raw: unknown): CloudContext {
  if (!isRecord(raw)) {
    throw new Error('context response was not in the expected shape');
  }
  return parseCloudContext(raw);
}

export const contextsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    setCloudProviderAlias: builder.mutation<
      NoValue,
      { token: string; alias: string; input: CloudProviderAliasInput }
    >({
      query: ({ token, alias, input }) => ({
        url: `/v1/cloud-provider-aliases/${encodeURIComponent(alias)}`,
        method: 'PUT',
        body: input,
        token,
        label: 'alias request',
      }),
    }),

    createContext: builder.mutation<CloudContext, { token: string; input: CreateContextInput }>({
      query: ({ token, input }) => ({
        url: '/v1/contexts',
        method: 'POST',
        body: input,
        token,
        label: 'create context request',
      }),
      transformResponse: (raw: unknown) => {
        if (!isRecord(raw) || !isRecord(raw.context)) {
          throw new Error('create context response was not in the expected shape');
        }
        return parseCloudContext(raw.context);
      },
      invalidatesTags: ['Config'],
    }),

    // getContext polls one cloud context by id; the provision controller
    // drives this with `pollingInterval` until status reaches running/failed.
    getContext: builder.query<CloudContext, { token: string; contextId: string }>({
      query: ({ token, contextId }) => ({
        url: `/v1/contexts/${encodeURIComponent(contextId)}`,
        token,
        label: 'context request',
      }),
      transformResponse: parseCloudContextResponse,
      providesTags: (_result, _error, arg) => [{ type: 'Context', id: arg.contextId }],
    }),
  }),
});

export const { useSetCloudProviderAliasMutation, useCreateContextMutation, useGetContextQuery } =
  contextsApi;
