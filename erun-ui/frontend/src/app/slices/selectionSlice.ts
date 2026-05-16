import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

export interface SelectionState {
  selected: UISelection | null;
}

const initialState: SelectionState = {
  selected: null,
};

export const selectionSlice = createSlice({
  name: 'selection',
  initialState,
  reducers: {
    setSelected(state, action: PayloadAction<UISelection | null>) {
      state.selected = action.payload;
    },
  },
});

export const { setSelected } = selectionSlice.actions;
export default selectionSlice.reducer;
