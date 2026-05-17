import type { UIIdleStatus, UISelection } from '@/types';

import { LoadIdleStatus } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export const idleApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getIdleStatus: builder.query<UIIdleStatus, UISelection>({
      queryFn: wailsQueryFn<UISelection, UIIdleStatus>((selection) => LoadIdleStatus(selection)),
      providesTags: ['IdleStatus'],
    }),
  }),
});

export const { useGetIdleStatusQuery, useLazyGetIdleStatusQuery } = idleApi;
