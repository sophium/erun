import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { defaultGlobalConfigDialog, type GlobalConfigDialogState } from '../state';

const initialState: GlobalConfigDialogState = defaultGlobalConfigDialog();

export const globalConfigDialogSlice = createSlice({
  name: 'globalConfigDialog',
  initialState,
  reducers: {
    setGlobalConfigDialog(_state, action: PayloadAction<GlobalConfigDialogState>) {
      return action.payload;
    },
    patchGlobalConfigDialog(state, action: PayloadAction<Partial<GlobalConfigDialogState>>) {
      Object.assign(state, action.payload);
    },
    resetGlobalConfigDialog() {
      return defaultGlobalConfigDialog();
    },
  },
});

export const { setGlobalConfigDialog, patchGlobalConfigDialog, resetGlobalConfigDialog } =
  globalConfigDialogSlice.actions;
export default globalConfigDialogSlice.reducer;
