// PLACEHOLDER — not a working sign-in flow.
//
// The real flow is OIDC Authorization Code + PKCE against the platform issuer
// (Zitadel); it is deliberately stubbed here because it cannot be built or
// claimed working without a live IdP to verify against.
//
// Driving each env's per-env MCP is a later increment and is RCE-sensitive
// (its `raw` tool can `kubectl exec`), so it is out of scope here too.
//
// Until the real flow exists the token comes from a dev-only stub.

export function login(): never {
  throw new Error('OIDC login is not implemented yet (see TODO(#606) in src/auth/auth.ts)');
}

// Dev-only stub token so the read model is exercisable before the OIDC flow exists.
export function devBearerToken(): string | undefined {
  const token = import.meta.env.VITE_DEV_BEARER_TOKEN;
  return token && token.length > 0 ? token : undefined;
}
