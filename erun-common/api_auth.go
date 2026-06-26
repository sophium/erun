package eruncommon

// Hosted-API auth edge (issue #674). erun-backend-api, like the per-env MCP edge
// (#655/#656), is always authenticated via the token's `iss` and trusts the same
// two kinds of issuer through one verification flow:
//
//   - `file://<path>` — the desktop case: a self-contained Ed25519 keypair. The
//     desktop signs an EdDSA JWT with its private key, the matching public key is
//     injected into the API and named in the token's `iss` as `file://<path>`,
//     and the verifier loads the key from that path. EdDSA is hard-checked
//     (no alg-confusion). This is the path e2e tests use — no live IdP required.
//   - `https://…` — a real OIDC issuer (Zitadel / AWS STS), verified against the
//     issuer's published JWKS via the shared *OIDCVerifier.
//
// The desktop signs API tokens carrying APITokenAudience; the API enforces it on
// the file:// path. Reuse the shared edge verifier (VerifyMCPToken) for the
// file:// signature/alg/expiry/audience checks; the issuer string is derived
// with FileIssuer(path) from the configured mount path.

// APITokenAudience is the stable audience an API bearer token minted by the
// desktop must carry and the API enforces on the file:// path. It differs from
// the per-env MCPTokenAudience ("erun-mcp:<tenant>/<env>") so a token minted for
// an environment's MCP edge cannot be replayed against the central API, and vice
// versa.
const APITokenAudience = "erun-api"
