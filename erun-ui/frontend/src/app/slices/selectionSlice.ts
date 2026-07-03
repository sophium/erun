import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

export interface SelectionState {
  selected: UISelection | null;
  // pendingOpenAfterDeploy gates the create→deploy→open flow:
  // after `erun init` the desktop composes a deploy and records the new env
  // here; the env's tabs open only once the matching `environment-deployed`
  // signal arrives, never against a runtime that does not exist yet.
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
