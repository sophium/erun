import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

export type SidebarCloudAliasAction = '' | 'login' | 'logout' | 'bearer';

export interface SidebarState {
  collapsedTenants: string[];
  // sidebarCloudAliasBusyByAlias maps an alias to its current in-flight action.
  // Absence means idle. Keyed by alias so per-provider-type rows (AWS,
  // Cloudflare) show independent spinners: an AWS login and a Cloudflare token
  // re-verify can run at once without one disabling the other's control
  // (visibility of system status, Nielsen #1).
  sidebarCloudAliasBusyByAlias: Record<string, SidebarCloudAliasAction>;
}

const initialState: SidebarState = {
  collapsedTenants: [],
  sidebarCloudAliasBusyByAlias: {},
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
      action: PayloadAction<{ alias: string; busy: boolean; action: SidebarCloudAliasAction }>,
    ) {
      const alias = action.payload.alias.trim();
      if (!alias) {
        return;
      }
      if (action.payload.busy) {
        state.sidebarCloudAliasBusyByAlias[alias] = action.payload.action;
        return;
      }
      // Drop the alias's entry without a dynamic delete: rebuild the map
      // omitting it. Absence means idle.
      const next: Record<string, SidebarCloudAliasAction> = {};
      for (const [key, value] of Object.entries(state.sidebarCloudAliasBusyByAlias)) {
        if (key !== alias) {
          next[key] = value;
        }
      }
      state.sidebarCloudAliasBusyByAlias = next;
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
