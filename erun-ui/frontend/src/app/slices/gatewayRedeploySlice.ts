import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIGatewayRouting } from '@/uiOpenRouterTypes';

// Drives the "apply the gateway change" dialog listing envs still routed to the
// old gateway; this slice does not redeploy.
export interface GatewayRedeployState {
  open: boolean;
  items: UIGatewayRouting[];
}

const initialState: GatewayRedeployState = {
  open: false,
  items: [],
};

export const gatewayRedeploySlice = createSlice({
  name: 'gatewayRedeploy',
  initialState,
  reducers: {
    openGatewayRedeployDialog(state, action: PayloadAction<UIGatewayRouting[]>) {
      state.open = true;
      state.items = action.payload;
    },
    closeGatewayRedeployDialog() {
      return initialState;
    },
  },
});

export const { openGatewayRedeployDialog, closeGatewayRedeployDialog } =
  gatewayRedeploySlice.actions;
export default gatewayRedeploySlice.reducer;
