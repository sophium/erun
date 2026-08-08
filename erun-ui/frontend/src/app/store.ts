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
import { selectionSyncMiddleware } from './middleware/selectionSyncMiddleware';
import { terminalDisplayMiddleware } from './middleware/terminalDisplayMiddleware';
import { uiTraceMiddleware } from './middleware/uiTraceMiddleware';
import activityReducer from './slices/activitySlice';
import aiActivityReducer from './slices/aiActivitySlice';
import autoStartPromptReducer from './slices/autoStartPromptSlice';
import contributeReducer from './slices/contributeSlice';
import doctorReducer from './slices/doctorSlice';
import environmentDialogReducer from './slices/environmentDialogSlice';
import envStatusReducer from './slices/envStatusSlice';
import globalConfigDialogReducer from './slices/globalConfigDialogSlice';
import idleReducer from './slices/idleSlice';
import layoutReducer from './slices/layoutSlice';
import manageDialogReducer from './slices/manageDialogSlice';
import notificationReducer from './slices/notificationSlice';
import orchestratorsReducer from './slices/orchestratorsSlice';
import outputsDialogReducer from './slices/outputsDialogSlice';
import pinVersionReducer from './slices/pinVersionSlice';
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
import upgradeAllReducer from './slices/upgradeAllSlice';
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
    aiActivity: aiActivityReducer,
    envStatus: envStatusReducer,
    tenants: tenantsReducer,
    environmentDialog: environmentDialogReducer,
    manageDialog: manageDialogReducer,
    tenantDialog: tenantDialogReducer,
    tenantDashboard: tenantDashboardReducer,
    globalConfigDialog: globalConfigDialogReducer,
    autoStartPrompt: autoStartPromptReducer,
    contribute: contributeReducer,
    upgradeAll: upgradeAllReducer,
    outputsDialog: outputsDialogReducer,
    pinVersion: pinVersionReducer,
    orchestrators: orchestratorsReducer,
    [wailsApi.reducerPath]: wailsApi.reducer,
  },
  middleware: (getDefaultMiddleware) =>
    getDefaultMiddleware({
      thunk: { extraArgument: thunkExtra },
      // State stays serializable; Sets are rebuilt only at consumer
      // boundaries, so the default serializable check needs no Set whitelist.
    })
      .concat(wailsApi.middleware)
      .concat(persistenceMiddleware.middleware)
      .concat(selectionSyncMiddleware.middleware)
      .concat(terminalDisplayMiddleware.middleware)
      .concat(uiTraceMiddleware),
});

setupListeners(store.dispatch);

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
export type AppThunk<R = void> = ThunkAction<R, RootState, ThunkExtra, UnknownAction>;
