package mcpauth

// Hosted-API auth edge: file:// Ed25519 issuers (EdDSA hard-checked) and https:// OIDC issuers.

// APITokenAudience differs from MCPTokenAudience so tokens cannot be replayed between the API and an MCP edge.
const APITokenAudience = "erun-api"
