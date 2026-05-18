import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { type AutoStartPromptState, defaultAutoStartPrompt } from '../state';

const initialState: AutoStartPromptState = defaultAutoStartPrompt();

export const autoStartPromptSlice = createSlice({
  name: 'autoStartPrompt',
  initialState,
  reducers: {
    setAutoStartPrompt(_state, action: PayloadAction<AutoStartPromptState>) {
      return action.payload;
    },
    patchAutoStartPrompt(state, action: PayloadAction<Partial<AutoStartPromptState>>) {
      Object.assign(state, action.payload);
    },
    resetAutoStartPrompt() {
      return defaultAutoStartPrompt();
    },
  },
});

export const { setAutoStartPrompt, patchAutoStartPrompt, resetAutoStartPrompt } =
  autoStartPromptSlice.actions;
export default autoStartPromptSlice.reducer;
