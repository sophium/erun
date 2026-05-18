// Endpoints must be imported to inject themselves into wailsApi.
import './api/stateApi';
import './api/environmentApi';
import './api/tenantApi';
import './api/kubernetesApi';
import './api/cloudApi';
import './api/reviewApi';
import './api/idleApi';
import './api/sessionApi';
import './api/deployApi';
import './api/globalConfigApi';

import { configureStore, type ThunkAction, type UnknownAction } from '@reduxjs/toolkit';
import { setupListeners } from '@reduxjs/toolkit/query';

import { wailsApi } from './api/wailsApi';
import { persistenceMiddleware } from './middleware/persistenceMiddleware';
import { terminalDisplayMiddleware } from './middleware/terminalDisplayMiddleware';
import activityReducer from './slices/activitySlice';
import autoStartPromptReducer from './slices/autoStartPromptSlice';
import doctorReducer from './slices/doctorSlice';
import environmentDialogReducer from './slices/environmentDialogSlice';
import globalConfigDialogReducer from './slices/globalConfigDialogSlice';
import idleReducer from './slices/idleSlice';
import layoutReducer from './slices/layoutSlice';
import manageDialogReducer from './slices/manageDialogSlice';
import notificationReducer from './slices/notificationSlice';
import requestCountersReducer from './slices/requestCountersSlice';
import reviewReducer from './slices/reviewSlice';
import selectionReducer from './slices/selectionSlice';
import sessionsReducer from './slices/sessionsSlice';
import sidebarReducer from './slices/sidebarSlice';
import tenantDashboardReducer from './slices/tenantDashboardSlice';
import tenantDialogReducer from './slices/tenantDialogSlice';
import tenantsReducer from './slices/tenantsSlice';
import terminalReducer from './slices/terminalSlice';
import terminalStatusReducer from './slices/terminalStatusSlice';
import { type ThunkExtra, thunkExtra } from './thunkExtra';

export const store = configureStore({
  reducer: {
    selection: selectionReducer,
    layout: layoutReducer,
    sidebar: sidebarReducer,
    terminal: terminalReducer,
    terminalStatus: terminalStatusReducer,
    review: reviewReducer,
    notification: notificationReducer,
    requestCounters: requestCountersReducer,
    sessions: sessionsReducer,
    doctor: doctorReducer,
    idle: idleReducer,
    activity: activityReducer,
    tenants: tenantsReducer,
    environmentDialog: environmentDialogReducer,
    manageDialog: manageDialogReducer,
    tenantDialog: tenantDialogReducer,
    tenantDashboard: tenantDashboardReducer,
    globalConfigDialog: globalConfigDialogReducer,
    autoStartPrompt: autoStartPromptReducer,
    [wailsApi.reducerPath]: wailsApi.reducer,
  },
  middleware: (getDefaultMiddleware) =>
    getDefaultMiddleware({
      thunk: { extraArgument: thunkExtra },
      // Slices intentionally store serializable shapes; the legacy AppState
      // selector reassembles Set instances at the consumer boundary, so we
      // do not need redux-toolkit's serializable check to whitelist Set.
    })
      .concat(wailsApi.middleware)
      .concat(persistenceMiddleware.middleware)
      .concat(terminalDisplayMiddleware.middleware),
});

setupListeners(store.dispatch);

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
export type AppThunk<R = void> = ThunkAction<R, RootState, ThunkExtra, UnknownAction>;
