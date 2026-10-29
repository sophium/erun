// Endpoints must be imported to inject themselves into platformApi.
import './api/configApi';
import './api/contextsApi';
import './api/environmentsApi';
import './api/identityApi';
import './api/mcpApi';

import { configureStore, type ThunkAction, type UnknownAction } from '@reduxjs/toolkit';
import { setupListeners } from '@reduxjs/toolkit/query';

import { platformApi } from './api/platformApi';
import authReducer from './slices/authSlice';

// A factory so each test gets an isolated RTK Query cache.
export function createAppStore() {
  const appStore = configureStore({
    reducer: {
      auth: authReducer,
      [platformApi.reducerPath]: platformApi.reducer,
    },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(platformApi.middleware),
    // 'tick' batches via queueMicrotask; the default 'raf' callback can outlive a jsdom
    // window and throw at teardown.
    enhancers: (getDefaultEnhancers) => getDefaultEnhancers({ autoBatch: { type: 'tick' } }),
  });
  setupListeners(appStore.dispatch);
  return appStore;
}

export const store = createAppStore();

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
export type AppThunk<R = void> = ThunkAction<R, RootState, undefined, UnknownAction>;
