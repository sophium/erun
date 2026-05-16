import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { DoctorOutcome } from '../state';

export interface DoctorState {
  lastDoctorBySelection: Record<string, DoctorOutcome>;
}

const initialState: DoctorState = {
  lastDoctorBySelection: {},
};

export const doctorSlice = createSlice({
  name: 'doctor',
  initialState,
  reducers: {
    recordDoctorOutcome(state, action: PayloadAction<{ key: string; outcome: DoctorOutcome }>) {
      state.lastDoctorBySelection[action.payload.key] = action.payload.outcome;
    },
  },
});

export const { recordDoctorOutcome } = doctorSlice.actions;
export default doctorSlice.reducer;
