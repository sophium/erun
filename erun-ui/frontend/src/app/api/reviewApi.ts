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

// DirectoryDiffArgs addresses a path rather than an environment.
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
    // Reads the directory with host git; separate from getDiff because RTK Query
    // keys queries by args.
    getDirectoryDiff: builder.query<DiffResult, DirectoryDiffArgs>({
      queryFn: wailsQueryFn<DirectoryDiffArgs, DiffResult>(
        ({ directory, options }) => LoadDirectoryDiff(directory, options) as Promise<DiffResult>,
      ),
      providesTags: ['Diff'],
    }),
  }),
});

export const { useGetDiffQuery, useLazyGetDiffQuery } = reviewApi;
