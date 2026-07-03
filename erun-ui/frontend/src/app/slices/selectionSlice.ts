import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

export interface SelectionState {
  selected: UISelection | null;
  // Defers opening a newly created env's tabs until its deploy completes;
  // opening against a runtime that does not exist yet would fail.
  pendingOpenAfterDeploy: UISelection | null;
}

const initialState: SelectionState = {
  selected: null,
  pendingOpenAfterDeploy: null,
};

export const selectionSlice = createSlice({
  name: 'selection',
  initialState,
  reducers: {
    setSelected(state, action: PayloadAction<UISelection | null>) {
      state.selected = action.payload;
    },
    setPendingOpenAfterDeploy(state, action: PayloadAction<UISelection>) {
      state.pendingOpenAfterDeploy = action.payload;
    },
    clearPendingOpenAfterDeploy(state) {
      state.pendingOpenAfterDeploy = null;
    },
  },
});

export const { setSelected, setPendingOpenAfterDeploy, clearPendingOpenAfterDeploy } =
  selectionSlice.actions;
export default selectionSlice.reducer;
