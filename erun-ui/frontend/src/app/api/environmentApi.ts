import type {
  UIEnvironmentConfig,
  UIExposedService,
  UIExposeServiceInput,
  UIExposureList,
  UIRuntimeResourceStatus,
  UISelection,
  UIUnexposeResult,
  UIVersionSuggestions,
} from '@/types';
import type { UIClusterRegistryStatus, UIEnvironmentHealth } from '@/uiDiagnosticsTypes';
import type { UIEnvironmentStopResult } from '@/uiLifecycleTypes';
import type {
  UIRuntimeActivity,
  UIRuntimeReclaimInput,
  UIRuntimeReclaimResult,
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
  LoadRuntimeActivity,
  LoadRuntimeResourceStatus,
  LoadRuntimeUsage,
  LoadVersionSuggestions,
  ReclaimRuntimeResources,
  SaveEnvironmentConfig,
  StopEnvironment,
  UnexposeEnvironment,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

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

export const environmentApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
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
    // Stopping frees the env's runtime and dind limits, so the node capacity
    // the Runtime tab offers every other env changes the moment it lands —
    // invalidate the resource status rather than leaving stale maxima on screen.
    stopEnvironment: builder.mutation<UIEnvironmentStopResult, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentStopResult>((selection) =>
        StopEnvironment(selection),
      ),
      invalidatesTags: ['RuntimeResourceStatus', 'RuntimeActivity', 'RuntimeUsage', 'AppState'],
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
    exposeEnvironmentService: builder.mutation<UIExposedService, ExposeServiceArgs>({
      queryFn: wailsQueryFn<ExposeServiceArgs, UIExposedService>(({ selection, input }) =>
        ExposeEnvironmentService(selection, input),
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
} = environmentApi;
