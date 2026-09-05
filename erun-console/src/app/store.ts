// Endpoints must be imported to inject themselves into platformApi, mirroring
// erun-ui/frontend's app/store.ts.
import './api/configApi';
import './api/contextsApi';
import './api/environmentsApi';
import './api/identityApi';
import './api/mcpApi';

import { configureStore, type ThunkAction, type UnknownAction } from '@reduxjs/toolkit';
import { setupListeners } from '@reduxjs/toolkit/query';

import { platformApi } from './api/platformApi';
import authReducer from './slices/authSlice';

// createAppStore is a factory rather than a bare singleton so tests can build
// an isolated store per render — the RTK Query cache lives on the store, and
// reusing one store across tests would let one test's mocked-fetch response
// leak into the next via the cache.
export function createAppStore() {
  const appStore = configureStore({
    reducer: {
      auth: authReducer,
      [platformApi.reducerPath]: platformApi.reducer,
    },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(platformApi.middleware),
    // The default 'raf' autoBatch enhancer schedules a window.requestAnimationFrame
    // callback plus a setTimeout fallback on every dispatch against the
    // global window/cancelAnimationFrame. A store built for a jsdom test
    // (renderWithStore.tsx makes a fresh one per render) can outlive that
    // window, so a callback still pending at teardown throws. 'tick' batches
    // via queueMicrotask, which always drains within the dispatching test.
    enhancers: (getDefaultEnhancers) => getDefaultEnhancers({ autoBatch: { type: 'tick' } }),
  });
  setupListeners(appStore.dispatch);
  return appStore;
}

export const store = createAppStore();

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
export type AppThunk<R = void> = ThunkAction<R, RootState, undefined, UnknownAction>;
