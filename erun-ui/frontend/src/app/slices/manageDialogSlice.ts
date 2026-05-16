import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { defaultManageDialog, type ManageDialogState } from '../state';

const initialState: ManageDialogState = defaultManageDialog();

export const manageDialogSlice = createSlice({
  name: 'manageDialog',
  initialState,
  reducers: {
    setManageDialog(_state, action: PayloadAction<ManageDialogState>) {
      return action.payload;
    },
    patchManageDialog(state, action: PayloadAction<Partial<ManageDialogState>>) {
      Object.assign(state, action.payload);
    },
    resetManageDialog() {
      return defaultManageDialog();
    },
  },
});

export const { setManageDialog, patchManageDialog, resetManageDialog } =
  manageDialogSlice.actions;
export default manageDialogSlice.reducer;
