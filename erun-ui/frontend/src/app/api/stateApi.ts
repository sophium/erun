import type { UIState } from '@/types';

import { LoadState } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export const stateApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getInitialState: builder.query<UIState, void>({
      queryFn: wailsQueryFn<void, UIState>(() => LoadState() as Promise<UIState>),
      providesTags: ['AppState'],
    }),
  }),
});

export const { useGetInitialStateQuery, useLazyGetInitialStateQuery } = stateApi;
