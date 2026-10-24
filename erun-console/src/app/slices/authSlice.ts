import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { OidcConfig } from '../../auth/auth';

// `tokenError` is set only when resolving the token failed, not when signed out.
export interface AuthState {
  oidcResolved: boolean;
  oidc?: OidcConfig;
  oidcFallbackReason?: string;
  status: 'resolving' | 'signed-out' | 'authenticated';
  token?: string;
  tokenError?: string;
}

const initialState: AuthState = {
  oidcResolved: false,
  status: 'resolving',
};

export const authSlice = createSlice({
  name: 'auth',
  initialState,
  reducers: {
    setOidcResolved(
      state,
      action: PayloadAction<{ config?: OidcConfig; fallbackReason?: string }>,
    ) {
      state.oidcResolved = true;
      state.oidc = action.payload.config;
      state.oidcFallbackReason = action.payload.fallbackReason;
    },
    setAuthenticated(state, action: PayloadAction<string>) {
      state.status = 'authenticated';
      state.token = action.payload;
      state.tokenError = undefined;
    },
    setSignedOut(state) {
      state.status = 'signed-out';
      state.token = undefined;
    },
    setTokenError(state, action: PayloadAction<string>) {
      state.status = 'signed-out';
      state.token = undefined;
      state.tokenError = action.payload;
    },
    clearAuth(state) {
      state.status = 'signed-out';
      state.token = undefined;
      state.tokenError = undefined;
    },
  },
});

export const { setOidcResolved, setAuthenticated, setSignedOut, setTokenError, clearAuth } =
  authSlice.actions;
export default authSlice.reducer;
