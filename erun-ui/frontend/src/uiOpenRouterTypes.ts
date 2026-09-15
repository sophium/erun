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

// UIGatewayModel is one model the configured gateway advertises, read from the
// same /v1/models endpoint Claude Code's own model discovery uses, so the
// catalog can offer ids instead of asking the operator to type them.
export interface UIGatewayModel {
  id: string;
  displayName?: string;
  description?: string;
  // The window the gateway reports for this id, when it reports one at all.
  // The standard discovery response carries none, so this is absent for a
  // gateway that does not volunteer it.
  context?: number;
}

// UIHostGatewayDefaults is the gateway this machine's own Claude Code already
// routes through, read from the operator's user settings. It is offered as the
// catalog's starting point so a gateway already configured once is not typed
// again. It carries no credential: settings hold a token value, while the
// catalog names a Secret the pod resolves.
export interface UIHostGatewayDefaults {
  baseUrl?: string;
  model?: string;
  context?: number;
}

// UIGatewayCredentialCandidate is a Secret the gateway credential could come
// from, so the catalog offers the names that exist instead of asking for one to
// be invented. Only names are carried; a Secret's value is never read.
export interface UIGatewayCredentialCandidate {
  name: string;
  namespaces?: string[];
  keys?: string[];
}

export interface UIGatewayCredentialCandidates {
  candidates: UIGatewayCredentialCandidate[];
  // Namespaces that could not be read: a Secret that exists but whose access is
  // denied must not read as a Secret that is missing.
  problems?: string[];
  namespaces: number;
}
