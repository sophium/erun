import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIIdleStatus } from '@/types';

import type { CloudNodeOperation } from '../model';

export interface IdleState {
  idleStatus: UIIdleStatus | null;
  // cloudNodeOperations is the power work this desktop has in flight, keyed by
  // the node's own context name. It replaced a single global boolean, which
  // had no way to say WHICH node was busy: the titlebar's name follows the
  // selected environment while the flag did not, so the widget paired a
  // freshly-selected environment's node with an operation started elsewhere and
  // announced work on an environment that had none. Keyed this way, a label can
  // only ever report the operation on the node it names.
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
