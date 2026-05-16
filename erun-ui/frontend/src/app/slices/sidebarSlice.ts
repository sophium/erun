import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

export type SidebarCloudAliasAction = '' | 'login' | 'logout' | 'bearer';

export interface SidebarState {
  collapsedTenants: string[];
  sidebarCloudAliasBusy: boolean;
  sidebarCloudAliasAction: SidebarCloudAliasAction;
}

const initialState: SidebarState = {
  collapsedTenants: [],
  sidebarCloudAliasBusy: false,
  sidebarCloudAliasAction: '',
};

export const sidebarSlice = createSlice({
  name: 'sidebar',
  initialState,
  reducers: {
    toggleTenantCollapsed(state, action: PayloadAction<string>) {
      const tenant = action.payload;
      const idx = state.collapsedTenants.indexOf(tenant);
      if (idx >= 0) {
        state.collapsedTenants.splice(idx, 1);
      } else {
        state.collapsedTenants.push(tenant);
      }
    },
    setSidebarCloudAliasBusy(
      state,
      action: PayloadAction<{ busy: boolean; action: SidebarCloudAliasAction }>,
    ) {
      state.sidebarCloudAliasBusy = action.payload.busy;
      state.sidebarCloudAliasAction = action.payload.action;
    },
    setAll(_state, action: PayloadAction<SidebarState>) {
      return action.payload;
    },
  },
});

export const {
  toggleTenantCollapsed,
  setSidebarCloudAliasBusy,
  setAll: setSidebarAll,
} = sidebarSlice.actions;
export default sidebarSlice.reducer;
