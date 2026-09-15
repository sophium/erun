// The erun-level gateway catalog: the gateway an environment's Claude Code is
// routed through, and the models selectable from it. It lives in root config
// rather than a per-environment setting because it is one list the operator
// maintains and every environment selects from it.

export interface UIOpenRouterModel {
  id: string;
  // The context window Claude Code must assume for this id. A gateway id
  // carries none of its own, and the real windows differ between models, so one
  // environment-wide value would be wrong for at least one of them. Must be the
  // provider-level figure, which can be smaller than an advertised maximum:
  // declaring the larger headline lets a conversation grow past what the
  // serving provider accepts.
  context?: number;
}

export interface UIOpenRouterConfig {
  baseUrl?: string;
  // The credential is a Kubernetes Secret name and key, never a value, so
  // config stays safe to back up and share and nothing sensitive reaches helm
  // argv or a launch command.
  authTokenSecret?: string;
  authTokenKey?: string;
  defaultModel?: string;
  models?: UIOpenRouterModel[];
}
