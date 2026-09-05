import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { type CreateReviewDialogState, defaultCreateReviewDialog } from '../reviewWriteState';

const initialState: CreateReviewDialogState = defaultCreateReviewDialog();

export const createReviewDialogSlice = createSlice({
  name: 'createReviewDialog',
  initialState,
  reducers: {
    patchCreateReviewDialog(state, action: PayloadAction<Partial<CreateReviewDialogState>>) {
      Object.assign(state, action.payload);
    },
    resetCreateReviewDialog() {
      return defaultCreateReviewDialog();
    },
  },
});

export const { patchCreateReviewDialog, resetCreateReviewDialog } = createReviewDialogSlice.actions;
export default createReviewDialogSlice.reducer;
