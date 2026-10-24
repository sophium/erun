// The erun-level gateway catalog, kept in root config.

export interface UIOpenRouterModel {
  id: string;
  // Per-model window; must be the provider-level figure, not the advertised max.
  context?: number;
  // Claude Code never echoes reasoning back, so such a model is kept out of the
  // selectable set and out of ANTHROPIC_MODEL.
  requiresReasoningEcho?: boolean;
}

export interface UIOpenRouterConfig {
  baseUrl?: string;
  // A secret-store ref, never the token, so config stays shareable.
  authTokenRef?: string;
  defaultModel?: string;
  models?: UIOpenRouterModel[];
}

// Read from the same /v1/models endpoint Claude Code uses.
export interface UIGatewayModel {
  id: string;
  displayName?: string;
  description?: string;
  // Absent when the gateway does not report one.
  context?: number;
}

export interface UIHostGatewayDefaults {
  baseUrl?: string;
  model?: string;
  context?: number;
}

// Identifies the credential without revealing it.
export interface UIGatewayCredentialStatus {
  ref: string;
  // 'saved' | 'host'; absent means a deploy cannot authenticate. Plain string
  // because the generated binding cannot carry a union.
  source?: string;
  hint?: string;
  // With an absent source, says the host key belongs to another gateway.
  hostEndpoint?: string;
}

// The gateway is rendered at deploy time, so a change reaches the pod only
// through a redeploy.
export interface UIGatewayRouting {
  tenant: string;
  environment: string;
  // "" for Anthropic directly.
  deployed: string;
  wanted: string;
}
