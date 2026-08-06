import type {
  UIEnvironmentConfig,
  UIRuntimeResourceStatus,
  UISelection,
  UIVersionSuggestions,
} from '@/types';
import type { UIClusterRegistryStatus, UIEnvironmentHealth } from '@/uiDiagnosticsTypes';
import type { UIEnvironmentStopResult } from '@/uiLifecycleTypes';

import {
  CheckEnvironmentHealth,
  ChooseLocalRepoPath,
  ChooseWorkspaceSyncLocalFolder,
  DeleteEnvironment,
  LoadClusterRegistry,
  LoadEnvironmentConfig,
  LoadRuntimeResourceStatus,
  LoadVersionSuggestions,
  SaveEnvironmentConfig,
  StopEnvironment,
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
      invalidatesTags: ['RuntimeResourceStatus', 'AppState'],
    }),
    checkEnvironmentHealth: builder.mutation<UIEnvironmentHealth, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIEnvironmentHealth>((selection) =>
        CheckEnvironmentHealth(selection),
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
} = environmentApi;
