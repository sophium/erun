import { type Environment, isRecord, parseEnvironment, parseList } from 'erun-kit';

import { httpBaseQuery } from './httpBaseQuery';
import { platformApi } from './platformApi';

// The tenant is resolved from the token server-side and never sent.
export interface CreateEnvironmentInput {
  name: string;
  type: string;
  contextId?: string;
  kubernetesContext?: string;
  runtimeVersion?: string;
}

// 409 (deploy already in flight) and 501 (no deploy executor) resolve as data, not errors.
export type DeployOutcome =
  | { kind: 'accepted'; environment: Environment }
  | { kind: 'conflict' }
  | { kind: 'unavailable' };

function parseEnvironmentResponse(raw: unknown): Environment {
  if (!isRecord(raw)) {
    throw new Error('environment response was not in the expected shape');
  }
  return parseEnvironment(raw);
}

export const environmentsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // `tenantId` is operations-only and enforced server-side.
    listEnvironments: builder.query<Environment[], { token: string; tenantId?: string }>({
      query: ({ token, tenantId }) => ({
        url:
          tenantId === undefined
            ? '/v1/environments'
            : `/v1/environments?tenantId=${encodeURIComponent(tenantId)}`,
        token,
        label: 'list environments',
      }),
      transformResponse: (raw: unknown) => parseList(raw, parseEnvironment),
      providesTags: ['Environments'],
    }),

    createEnvironment: builder.mutation<
      Environment,
      { token: string; input: CreateEnvironmentInput }
    >({
      query: ({ token, input }) => ({
        url: '/v1/environments',
        method: 'POST',
        body: input,
        token,
        label: 'register environment request',
      }),
      transformResponse: parseEnvironmentResponse,
      invalidatesTags: ['Config', 'Environments'],
    }),

    getEnvironment: builder.query<Environment, { token: string; environmentId: string }>({
      query: ({ token, environmentId }) => ({
        url: `/v1/environments/${encodeURIComponent(environmentId)}`,
        token,
        label: 'environment request',
      }),
      transformResponse: parseEnvironmentResponse,
      providesTags: (_result, _error, arg) => [{ type: 'Environment', id: arg.environmentId }],
    }),

    deployEnvironment: builder.mutation<
      DeployOutcome,
      { token: string; environmentId: string; version?: string }
    >({
      async queryFn({ token, environmentId, version }, api, extraOptions) {
        const outcome = await httpBaseQuery(
          {
            url: `/v1/environments/${encodeURIComponent(environmentId)}/deploy`,
            method: 'POST',
            body: { version: version ?? '' },
            token,
            label: 'deploy request',
          },
          api,
          extraOptions,
        );
        if (outcome.data !== undefined) {
          return {
            data: { kind: 'accepted', environment: parseEnvironmentResponse(outcome.data) },
          };
        }
        if (outcome.error?.status === 409) {
          return { data: { kind: 'conflict' } };
        }
        if (outcome.error?.status === 501) {
          return { data: { kind: 'unavailable' } };
        }
        return { error: outcome.error ?? { message: 'deploy request failed' } };
      },
      invalidatesTags: (result, _error, arg) =>
        result?.kind === 'accepted'
          ? [{ type: 'Environment', id: arg.environmentId }, 'Config', 'Environments']
          : [],
    }),
  }),
});

export const {
  useListEnvironmentsQuery,
  useCreateEnvironmentMutation,
  useGetEnvironmentQuery,
  useDeployEnvironmentMutation,
} = environmentsApi;
