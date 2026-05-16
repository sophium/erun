import { LoadState } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';
import type { UIState } from '@/types';

export const stateApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getInitialState: builder.query<UIState, void>({
      queryFn: wailsQueryFn<void, UIState>(() => LoadState() as Promise<UIState>),
      providesTags: ['AppState'],
    }),
  }),
});

export const { useGetInitialStateQuery, useLazyGetInitialStateQuery } = stateApi;
