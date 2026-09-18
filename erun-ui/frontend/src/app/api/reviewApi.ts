import type { DiffResult, UISelection } from '@/types';

import { LoadDiff, LoadDirectoryDiff } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export interface UIDiffOptions {
  scope?: string;
  selectedCommit?: string;
  target?: string;
}

interface DiffArgs {
  selection: UISelection;
  options: UIDiffOptions;
}

// DirectoryDiffArgs is DiffArgs for a target that is a path rather than an
// environment: no tenant, no environment, no MCP edge to call.
interface DirectoryDiffArgs {
  directory: string;
  options: UIDiffOptions;
}

export const reviewApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getDiff: builder.query<DiffResult, DiffArgs>({
      queryFn: wailsQueryFn<DiffArgs, DiffResult>(
        ({ selection, options }) => LoadDiff(selection, options) as Promise<DiffResult>,
      ),
      providesTags: ['Diff'],
    }),
    // getDirectoryDiff is the path-addressed sibling of getDiff: the same
    // result, fetched by reading the directory's own working tree with host git
    // instead of a pod's MCP edge. It is a separate endpoint rather than a branch
    // inside one queryFn because the two take different arguments, and RTK
    // Query keys a query by its args.
    getDirectoryDiff: builder.query<DiffResult, DirectoryDiffArgs>({
      queryFn: wailsQueryFn<DirectoryDiffArgs, DiffResult>(
        ({ directory, options }) => LoadDirectoryDiff(directory, options) as Promise<DiffResult>,
      ),
      providesTags: ['Diff'],
    }),
  }),
});

export const { useGetDiffQuery, useLazyGetDiffQuery } = reviewApi;
