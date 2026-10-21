import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIGatewayRouting } from '@/uiOpenRouterTypes';

// Drives the "apply the gateway change" dialog: the environments whose running
// pod still routes where it was deployed to, after the gateway was switched in
// ERun settings. Confirming redeploys them; this slice does not.
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
