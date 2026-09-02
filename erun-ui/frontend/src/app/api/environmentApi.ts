import type {
  UIEnvironmentConfig,
  UIExposePreview,
  UIExposeServiceInput,
  UIExposeServiceResult,
  UIExposureList,
  UIRuntimeResourceStatus,
  UISelection,
  UIUnexposeResult,
  UIVersionSuggestions,
} from '@/types';
import type {
  UIClusterRegistryStatus,
  UIEnvironmentHealth,
  UIHostedRegistryStatus,
} from '@/uiDiagnosticsTypes';
import type { UIEnvironmentStopResult } from '@/uiLifecycleTypes';
import type {
  UIRuntimeActivity,
  UIRuntimeReclaimInput,
  UIRuntimeReclaimResult,
  UIRuntimeSizingRecommendation,
  UIRuntimeUsage,
} from '@/uiRuntimeTypes';

import {
  CheckEnvironmentHealth,
  ChooseLocalRepoPath,
  ChooseWorkspaceSyncLocalFolder,
  DeleteEnvironment,
  ExposeEnvironmentService,
  ListEnvironmentExposures,
  LoadClusterRegistry,
  LoadEnvironmentConfig,
  LoadHostedRegistry,
  LoadRuntimeActivity,
  LoadRuntimeResourceStatus,
  LoadRuntimeSizing,
  LoadRuntimeUsage,
  LoadVersionSuggestions,
  PreviewExposeEnvironmentService,
  ReclaimRuntimeResources,
  ResizeRuntimeToRecommendation,
  SaveEnvironmentConfig,
  StopEnvironment,
  UnexposeEnvironment,
} from '../../../wailsjs/go/main/App';
import type { EnvironmentApiBuilder } from './wailsApi';
import { wailsApi } from './wailsApi';
import { type NoValue, wailsQueryFn } from './wailsBaseQuery';

interface SaveEnvArgs {
  selection: UISelection;
  config: UIEnvironmentConfig;
}

interface DeleteEnvArgs {
  selection: UISelection;
  confirmation: string;
}

interface WorkspaceSyncArgs {
  selection: UISelection;
  current: string;
}

interface RuntimeResourceArgs {
  kubernetesContext: string;
  tenant?: string;
  environment?: string;
}

interface ExposeServiceArgs {
  selection: UISelection;
  input: UIExposeServiceInput;
}

// Split out of the main endpoints map to keep it within its line budget: the
// node-capacity reading, this environment's own usage/sizing/activity
// readings, and the reclaim/resize actions that act on them all belong
// together as "what the Runtime tab shows and does".
function runtimeTabEndpoints(builder: EnvironmentApiBuilder) {
  return {
    getRuntimeResourceStatus: builder.query<UIRuntimeResourceStatus, RuntimeResourceArgs>({
      queryFn: wailsQueryFn<RuntimeResourceArgs, UIRuntimeResourceStatus>((args) =>
        LoadRuntimeResourceStatus(args),
      ),
      providesTags: ['RuntimeResourceStatus'],
    }),
    getClusterRegistry: builder.query<UIClusterRegistryStatus, RuntimeResourceArgs>({
      queryFn: wailsQueryFn<RuntimeResourceArgs, UIClusterRegistryStatus>((args) =>
        LoadClusterRegistry(args),
      ),
      providesTags: ['RuntimeResourceStatus'],
    }),
    // What the runtime pod is running right now: sessions and the processes
    // holding memory. Read-only — nothing here reclaims anything.
    getRuntimeActivity: builder.query<UIRuntimeActivity, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIRuntimeActivity>((selection) =>
        LoadRuntimeActivity(selection),
      ),
      providesTags: ['RuntimeActivity'],
    }),
    // A reclaim changes both what the pod holds and what the node has free, so
    // it invalidates the activity reading and the capacity figures together —
    // the operator must be able to see the effect of what they just did.
    reclaimRuntimeResources: builder.mutation<UIRuntimeReclaimResult, UIRuntimeReclaimInput>({
      queryFn: wailsQueryFn<UIRuntimeReclaimInput, UIRuntimeReclaimResult>((input) =>
        ReclaimRuntimeResources(input),
      ),
      invalidatesTags: ['RuntimeActivity', 'RuntimeResourceStatus'],
    }),
    // This environment's own CPU, memory and disk usage against its cgroup
    // limits — the reading that turns the sliders above from a guess into a
    // decision.
    getRuntimeUsage: builder.query<UIRuntimeUsage, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIRuntimeUsage>((selection) =>
        LoadRuntimeUsage(selection),
      ),
      providesTags: ['RuntimeUsage'],
    }),
    // The environment's own opinion of its size — read inside the pod (see
    // LoadRuntimeSizing's comment for why this cannot be a host-side read the
    // way getRuntimeUsage is).
    getRuntimeSizing: builder.query<UIRuntimeSizingRecommendation, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIRuntimeSizingRecommendation>((selection) =>
        LoadRuntimeSizing(selection),
      ),
      providesTags: ['RuntimeSizing'],
    }),
    // Applies the standing recommendation for real. Rolls the pod, so it
    // invalidates the same tags a redeploy would; the sizing reading itself is
    // also re-fetched since a successful resize changes what it reports.
    resizeRuntimeToRecommendation: builder.mutation<
      UIRuntimeSizingRecommendation,
      { selection: UISelection; overrideLease: boolean }
    >({
      queryFn: wailsQueryFn<
        { selection: UISelection; overrideLease: boolean },
        UIRuntimeSizingRecommendation
      >(({ selection, overrideLease }) => ResizeRuntimeToRecommendation(selection, overrideLease)),
      invalidatesTags: ['RuntimeSizing', 'RuntimeUsage', 'RuntimeResourceStatus', 'AppState'],
    }),
  };
}

export const environmentApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    ...runtimeTabEndpoints(builder),
    getEnvironmentConfig: builder.query<UIEnvironmentConfig, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentConfig>((selection) =>
        LoadEnvironmentConfig(selection),
      ),
      providesTags: (_result, _error, selection) => [
        { type: 'EnvironmentConfig', id: `${selection.tenant}/${selection.environment}` },
      ],
    }),
    saveEnvironmentConfig: builder.mutation<UIEnvironmentConfig, SaveEnvArgs>({
      queryFn: wailsQueryFn<SaveEnvArgs, UIEnvironmentConfig>(({ selection, config }) =>
        SaveEnvironmentConfig(selection, config as never),
      ),
      invalidatesTags: (_result, _error, { selection }) => [
        { type: 'EnvironmentConfig', id: `${selection.tenant}/${selection.environment}` },
        'AppState',
      ],
    }),
    deleteEnvironment: builder.mutation<unknown, DeleteEnvArgs>({
      queryFn: wailsQueryFn<DeleteEnvArgs, unknown>(({ selection, confirmation }) =>
        DeleteEnvironment(selection, confirmation),
      ),
      invalidatesTags: ['AppState'],
    }),
    chooseWorkspaceSyncLocalFolder: builder.mutation<string, WorkspaceSyncArgs>({
      queryFn: wailsQueryFn<WorkspaceSyncArgs, string>(({ selection, current }) =>
        ChooseWorkspaceSyncLocalFolder(selection, current),
      ),
    }),
    chooseLocalRepoPath: builder.mutation<string, string>({
      queryFn: wailsQueryFn<string, string>((current) => ChooseLocalRepoPath(current)),
    }),
    getVersionSuggestions: builder.query<UIVersionSuggestions, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIVersionSuggestions>((selection) =>
        LoadVersionSuggestions(selection),
      ),
      providesTags: ['VersionSuggestions'],
    }),
    // Probes erun's hosted container registry (registry.erunpaas.com), so the
    // new-environment dialog can gate "Use erun's hosted registry" on a real
    // check instead of offering it unconditionally.
    getHostedRegistry: builder.query<UIHostedRegistryStatus, NoValue>({
      queryFn: wailsQueryFn<NoValue, UIHostedRegistryStatus>(() => LoadHostedRegistry()),
    }),
    // Stopping frees the env's runtime and dind limits, so the node capacity
    // the Runtime tab offers every other env changes the moment it lands —
    // invalidate the resource status rather than leaving stale maxima on screen.
    stopEnvironment: builder.mutation<UIEnvironmentStopResult, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentStopResult>((selection) =>
        StopEnvironment(selection),
      ),
      invalidatesTags: ['RuntimeResourceStatus', 'RuntimeActivity', 'RuntimeUsage', 'AppState'],
    }),
    checkEnvironmentHealth: builder.mutation<UIEnvironmentHealth, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentHealth>((selection) =>
        CheckEnvironmentHealth(selection),
      ),
    }),
    // The Ports tab's exposure list. A mutation (not a query) like
    // checkEnvironmentHealth: it re-reads the cluster each time it is
    // dispatched rather than caching against a key, since exposing or
    // un-exposing invalidates it immediately below.
    listEnvironmentExposures: builder.mutation<UIExposureList, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIExposureList>((selection) =>
        ListEnvironmentExposures(selection),
      ),
    }),
    exposeEnvironmentService: builder.mutation<UIExposeServiceResult, ExposeServiceArgs>({
      queryFn: wailsQueryFn<ExposeServiceArgs, UIExposeServiceResult>(({ selection, input }) =>
        ExposeEnvironmentService(selection, input),
      ),
    }),
    // Resolves the hostname/scheme a real exposeEnvironmentService call would
    // produce, without applying anything -- lets the form show it before the
    // operator commits (issue #1906).
    previewExposeEnvironmentService: builder.mutation<UIExposePreview, ExposeServiceArgs>({
      queryFn: wailsQueryFn<ExposeServiceArgs, UIExposePreview>(({ selection, input }) =>
        PreviewExposeEnvironmentService(selection, input),
      ),
    }),
    unexposeEnvironment: builder.mutation<UIUnexposeResult, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIUnexposeResult>((selection) =>
        UnexposeEnvironment(selection),
      ),
    }),
  }),
});

export const {
  useGetEnvironmentConfigQuery,
  useLazyGetEnvironmentConfigQuery,
  useSaveEnvironmentConfigMutation,
  useDeleteEnvironmentMutation,
  useChooseWorkspaceSyncLocalFolderMutation,
  useChooseLocalRepoPathMutation,
  useGetVersionSuggestionsQuery,
  useLazyGetVersionSuggestionsQuery,
  useGetRuntimeResourceStatusQuery,
  useLazyGetRuntimeResourceStatusQuery,
  useGetRuntimeActivityQuery,
  useReclaimRuntimeResourcesMutation,
  useGetRuntimeUsageQuery,
  useGetRuntimeSizingQuery,
  useResizeRuntimeToRecommendationMutation,
} = environmentApi;
