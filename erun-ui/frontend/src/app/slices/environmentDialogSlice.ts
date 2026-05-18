import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { defaultEnvironmentDialog, type EnvironmentDialogState } from '../state';

const initialState: EnvironmentDialogState = defaultEnvironmentDialog();

export const environmentDialogSlice = createSlice({
  name: 'environmentDialog',
  initialState,
  reducers: {
    setEnvironmentDialog(_state, action: PayloadAction<EnvironmentDialogState>) {
      return action.payload;
    },
    patchEnvironmentDialog(state, action: PayloadAction<Partial<EnvironmentDialogState>>) {
      Object.assign(state, action.payload);
    },
    resetEnvironmentDialog() {
      return defaultEnvironmentDialog();
    },
  },
});

export const { setEnvironmentDialog, patchEnvironmentDialog, resetEnvironmentDialog } =
  environmentDialogSlice.actions;
export default environmentDialogSlice.reducer;
