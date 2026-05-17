import type { UIState } from '@/types';

import { LoadState } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { type NoValue, wailsQueryFn } from './wailsBaseQuery';

export const stateApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getInitialState: builder.query<UIState, NoValue>({
      queryFn: wailsQueryFn<NoValue, UIState>(() => LoadState() as Promise<UIState>),
      providesTags: ['AppState'],
    }),
  }),
});

export const { useGetInitialStateQuery, useLazyGetInitialStateQuery } = stateApi;
