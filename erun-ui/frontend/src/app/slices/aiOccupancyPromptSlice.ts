import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { type AIOccupancyPromptState, defaultAIOccupancyPrompt } from '../state';

const initialState: AIOccupancyPromptState = defaultAIOccupancyPrompt();

export const aiOccupancyPromptSlice = createSlice({
  name: 'aiOccupancyPrompt',
  initialState,
  reducers: {
    setAIOccupancyPrompt(_state, action: PayloadAction<AIOccupancyPromptState>) {
      return action.payload;
    },
    patchAIOccupancyPrompt(state, action: PayloadAction<Partial<AIOccupancyPromptState>>) {
      Object.assign(state, action.payload);
    },
    resetAIOccupancyPrompt() {
      return defaultAIOccupancyPrompt();
    },
  },
});

export const { setAIOccupancyPrompt, patchAIOccupancyPrompt, resetAIOccupancyPrompt } =
  aiOccupancyPromptSlice.actions;
export default aiOccupancyPromptSlice.reducer;
