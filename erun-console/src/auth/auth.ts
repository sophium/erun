// ============================================================================
// PLACEHOLDER — NOT A WORKING SIGN-IN FLOW.
// ============================================================================
//
// TODO(#606): OIDC Authorization Code + PKCE against the platform issuer
// (Zitadel). Not implemented — needs the live IdP to verify. The hosted console
// will redirect the Operator to the instance's OIDC issuer, complete the
// Authorization Code + PKCE exchange, and hold the resulting JWT to present as
// `Authorization: Bearer <jwt>` to erun-backend-api (the same token model the
// API's JWKS middleware already verifies — see
// erun-docs/docs/agent-reference/api-protocol.md § Sign-in). Building that flow
// requires a live Zitadel issuer to authenticate against, so it is deliberately
// left unimplemented in this first increment and cannot be claimed as working.
//
// TODO(#606): driving each environment's per-env MCP (reached at
// `mcp.<tenant>-<env>.services.<base-domain>`, behind the per-env auth edge — see
// api-protocol.md § "Per-env MCP edge authentication") is a later increment. It
// is RCE-sensitive (its `raw` tool can `kubectl exec`) and needs a live env to
// verify, so it is out of scope here.
//
// For now, so the read view is exercisable, the token comes from a dev source
// (a Vite env var). This is a stub for local development only.

export function login(): never {
  throw new Error('OIDC login is not implemented yet (see TODO(#606) in src/auth/auth.ts)');
}

// Dev-only token source so the read model can be exercised before the OIDC flow
// exists. Returns the token from VITE_DEV_BEARER_TOKEN, or undefined when unset.
// Replaced by the real PKCE-derived token once login() is implemented.
export function devBearerToken(): string | undefined {
  const token = import.meta.env.VITE_DEV_BEARER_TOKEN;
  return token && token.length > 0 ? token : undefined;
}
