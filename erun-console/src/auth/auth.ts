// PLACEHOLDER — not a working sign-in flow.
//
// The real flow is OIDC Authorization Code + PKCE against the platform issuer
// (Zitadel); it is deliberately stubbed here because it cannot be built or
// claimed working without a live IdP to verify against.
//
// The console can now mint and surface a per-env MCP bearer token (the backend
// signs it; see src/mcp/MCPAccessPanel), but actually driving that env's MCP
// tools over the live edge (mcp.<tenant>-<env>.services.<base-domain>) stays a
// later increment: it is RCE-sensitive (its `raw` tool can `kubectl exec`) and
// needs a deployed env carrying the backend's public key to verify against.
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
