import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIIdleStatus } from '@/types';

import type { CloudNodeOperation } from '../model';

export interface IdleState {
  idleStatus: UIIdleStatus | null;
  // Keyed by node context name so a label only reports work on the node it names.
  cloudNodeOperations: Record<string, CloudNodeOperation>;
}

const initialState: IdleState = {
  idleStatus: null,
  cloudNodeOperations: {},
};

export const idleSlice = createSlice({
  name: 'idle',
  initialState,
  reducers: {
    setIdleStatus(state, action: PayloadAction<UIIdleStatus | null>) {
      state.idleStatus = action.payload;
    },
    startCloudNodeOperation(
      state,
      action: PayloadAction<{ name: string; operation: CloudNodeOperation }>,
    ) {
      const name = action.payload.name.trim();
      if (name === '') {
        return;
      }
      state.cloudNodeOperations[name] = action.payload.operation;
    },
    finishCloudNodeOperation(state, action: PayloadAction<{ name: string }>) {
      Reflect.deleteProperty(state.cloudNodeOperations, action.payload.name.trim());
    },
  },
});

export const { finishCloudNodeOperation, setIdleStatus, startCloudNodeOperation } =
  idleSlice.actions;
export default idleSlice.reducer;
