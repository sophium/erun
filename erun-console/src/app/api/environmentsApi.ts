import { type Environment, isRecord, parseEnvironment } from 'erun-kit';

import { httpBaseQuery } from './httpBaseQuery';
import { platformApi } from './platformApi';

// The operator-authored fields to register an environment. The tenant is
// resolved from the caller's token server-side and is never sent from here.
export interface CreateEnvironmentInput {
  name: string;
  type: string;
  contextId?: string;
  kubernetesContext?: string;
  runtimeVersion?: string;
}

// DeployOutcome distinguishes the two states a caller must render specially
// from a genuine error: `conflict` (409 — a deploy is already in flight for
// this environment, a real expected state, not a failure) and `unavailable`
// (501 — the control plane has no deploy executor configured). Both resolve
// as data rather than as a query error, so the controller can render them
// without an error path.
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
      invalidatesTags: ['Config'],
    }),

    // getEnvironment polls one environment by id; the deploy controller drives
    // this with `pollingInterval` until status reaches running/failed.
    getEnvironment: builder.query<Environment, { token: string; environmentId: string }>({
      query: ({ token, environmentId }) => ({
        url: `/v1/environments/${encodeURIComponent(environmentId)}`,
        token,
        label: 'environment request',
      }),
      transformResponse: parseEnvironmentResponse,
      providesTags: (_result, _error, arg) => [{ type: 'Environment', id: arg.environmentId }],
    }),

    // deployEnvironment bypasses the shared error channel for 409/501: both
    // are expected outcomes the render layer shows inline, not query errors.
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
          ? [{ type: 'Environment', id: arg.environmentId }, 'Config']
          : [],
    }),
  }),
});

export const {
  useCreateEnvironmentMutation,
  useGetEnvironmentQuery,
  useDeployEnvironmentMutation,
} = environmentsApi;
