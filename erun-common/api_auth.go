package eruncommon

// Hosted-API auth edge. The API trusts two issuer kinds through one flow:
// a file:// Ed25519 keypair (the desktop's self-signed path, so e2e tests
// run with no live IdP) and a real https:// OIDC issuer (Zitadel / AWS STS).
// EdDSA is hard-checked so a swapped alg cannot confuse the verifier.

// APITokenAudience is the stable audience an API bearer token minted by the
// desktop must carry and the API enforces on the file:// path. It differs from
// the per-env MCPTokenAudience ("erun-mcp:<tenant>/<env>") so a token minted for
// an environment's MCP edge cannot be replayed against the central API, and vice
// versa.
const APITokenAudience = "erun-api"
