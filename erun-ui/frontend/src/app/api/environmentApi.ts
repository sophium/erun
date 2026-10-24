import type {
  UIEnvironmentConfig,
  UIEnvironmentServiceList,
  UIExposedService,
  UIExposeServiceInput,
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
  UIRuntimeRunState,
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
  ListEnvironmentServices,
  LoadClusterRegistry,
  LoadEnvironmentConfig,
  LoadHostedRegistry,
  LoadRuntimeActivity,
  LoadRuntimeResourceStatus,
  LoadRuntimeRunState,
  LoadRuntimeSizing,
  LoadRuntimeUsage,
  LoadVersionSuggestions,
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
    // Read before offering Stop, so stopping a stopped runtime is not a silent no-op.
    // One Deployment get, unlike the pod exec getRuntimeUsage needs.
    getRuntimeRunState: builder.query<UIRuntimeRunState, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIRuntimeRunState>((selection) =>
        LoadRuntimeRunState(selection),
      ),
      providesTags: ['RuntimeRunState'],
    }),
    getRuntimeActivity: builder.query<UIRuntimeActivity, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIRuntimeActivity>((selection) =>
        LoadRuntimeActivity(selection),
      ),
      providesTags: ['RuntimeActivity'],
    }),
    // Invalidates activity and capacity together so the effect is visible.
    reclaimRuntimeResources: builder.mutation<UIRuntimeReclaimResult, UIRuntimeReclaimInput>({
      queryFn: wailsQueryFn<UIRuntimeReclaimInput, UIRuntimeReclaimResult>((input) =>
        ReclaimRuntimeResources(input),
      ),
      invalidatesTags: ['RuntimeActivity', 'RuntimeResourceStatus'],
    }),
    getRuntimeUsage: builder.query<UIRuntimeUsage, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIRuntimeUsage>((selection) =>
        LoadRuntimeUsage(selection),
      ),
      providesTags: ['RuntimeUsage'],
    }),
    // Read inside the pod (see LoadRuntimeSizing).
    getRuntimeSizing: builder.query<UIRuntimeSizingRecommendation, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIRuntimeSizingRecommendation>((selection) =>
        LoadRuntimeSizing(selection),
      ),
      providesTags: ['RuntimeSizing'],
    }),
    // Rolls the pod, so it invalidates what a redeploy would, plus the sizing.
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
    // Gates the hosted-registry option on a real check.
    getHostedRegistry: builder.query<UIHostedRegistryStatus, NoValue>({
      queryFn: wailsQueryFn<NoValue, UIHostedRegistryStatus>(() => LoadHostedRegistry()),
    }),
    // Stopping frees node capacity and changes the run state Stop reads.
    stopEnvironment: builder.mutation<UIEnvironmentStopResult, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentStopResult>((selection) =>
        StopEnvironment(selection),
      ),
      invalidatesTags: [
        'RuntimeResourceStatus',
        'RuntimeActivity',
        'RuntimeUsage',
        'RuntimeRunState',
        'AppState',
      ],
    }),
    checkEnvironmentHealth: builder.mutation<UIEnvironmentHealth, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentHealth>((selection) =>
        CheckEnvironmentHealth(selection),
      ),
    }),
    // A mutation so it re-reads the cluster on every dispatch.
    listEnvironmentExposures: builder.mutation<UIExposureList, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIExposureList>((selection) =>
        ListEnvironmentExposures(selection),
      ),
    }),
    // A mutation so it re-reads the namespace on every dispatch.
    listEnvironmentServices: builder.mutation<UIEnvironmentServiceList, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentServiceList>((selection) =>
        ListEnvironmentServices(selection),
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
  useGetRuntimeRunStateQuery,
  useGetRuntimeSizingQuery,
  useResizeRuntimeToRecommendationMutation,
} = environmentApi;
