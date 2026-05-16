import { LoadDiff } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';
import type { DiffResult, UISelection } from '@/types';

export interface UIDiffOptions {
  scope?: string;
  selectedCommit?: string;
}

interface DiffArgs {
  selection: UISelection;
  options: UIDiffOptions;
}

export const reviewApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getDiff: builder.query<DiffResult, DiffArgs>({
      queryFn: wailsQueryFn<DiffArgs, DiffResult>(
        ({ selection, options }) => LoadDiff(selection, options as never) as Promise<DiffResult>,
      ),
      providesTags: ['Diff'],
    }),
  }),
});

export const { useGetDiffQuery, useLazyGetDiffQuery } = reviewApi;
