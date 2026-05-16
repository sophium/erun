import { LoadIdleStatus } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';
import type { UIIdleStatus, UISelection } from '@/types';

export const idleApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getIdleStatus: builder.query<UIIdleStatus, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIIdleStatus>(
        (selection) => LoadIdleStatus(selection) as Promise<UIIdleStatus>,
      ),
      providesTags: ['IdleStatus'],
    }),
  }),
});

export const { useGetIdleStatusQuery, useLazyGetIdleStatusQuery } = idleApi;
