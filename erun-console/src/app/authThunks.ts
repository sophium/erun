import { resolveOidcConfig, resolveToken } from '../auth/auth';
import { setAuthenticated, setOidcResolved, setSignedOut, setTokenError } from './slices/authSlice';
import type { AppThunk } from './store';

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'unexpected error';
}

// resolveAuth runs the console's sign-in lifecycle once on load: OIDC
// discovery (GET /v1/platform), then the bearer token (an OIDC callback
// exchange, a token already held this session, or the dev-token fallback).
export function resolveAuth(): AppThunk<Promise<void>> {
  return async (dispatch) => {
    const resolution = await resolveOidcConfig();
    dispatch(
      setOidcResolved({ config: resolution.config, fallbackReason: resolution.fallbackReason }),
    );
    try {
      const resolved = await resolveToken(resolution.config);
      if (resolved === undefined) {
        dispatch(setSignedOut());
        return;
      }
      dispatch(setAuthenticated(resolved));
    } catch (error: unknown) {
      dispatch(setTokenError(errorMessage(error)));
    }
  };
}
