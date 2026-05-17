import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { defaultTenantDialog, type TenantDialogState } from '../state';

const initialState: TenantDialogState = defaultTenantDialog();

export const tenantDialogSlice = createSlice({
  name: 'tenantDialog',
  initialState,
  reducers: {
    setTenantDialog(_state, action: PayloadAction<TenantDialogState>) {
      return action.payload;
    },
    patchTenantDialog(state, action: PayloadAction<Partial<TenantDialogState>>) {
      Object.assign(state, action.payload);
    },
    resetTenantDialog() {
      return defaultTenantDialog();
    },
  },
});

export const { setTenantDialog, patchTenantDialog, resetTenantDialog } = tenantDialogSlice.actions;
export default tenantDialogSlice.reducer;
