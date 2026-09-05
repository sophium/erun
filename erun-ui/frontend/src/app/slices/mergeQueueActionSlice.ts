import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { defaultMergeQueueAction, type MergeQueueActionState } from '../reviewWriteState';

const initialState: MergeQueueActionState = defaultMergeQueueAction();

export const mergeQueueActionSlice = createSlice({
  name: 'mergeQueueAction',
  initialState,
  reducers: {
    patchMergeQueueAction(state, action: PayloadAction<Partial<MergeQueueActionState>>) {
      Object.assign(state, action.payload);
    },
    resetMergeQueueAction() {
      return defaultMergeQueueAction();
    },
  },
});

export const { patchMergeQueueAction, resetMergeQueueAction } = mergeQueueActionSlice.actions;
export default mergeQueueActionSlice.reducer;
