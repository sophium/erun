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
  // Set when the model's provider demands the model's own reasoning back on the
  // next request. No erun AI lane can drive such a model: every lane drives
  // Claude Code, which builds the requests erun never builds and models no
  // reasoning_content at all, so a conversation on it is refused mid-run rather
  // than failing the work. Declaring one keeps it out of the selectable set and
  // out of the model an environment renders as ANTHROPIC_MODEL, so the failure
  // lands where the model is chosen instead of tens of turns in.
  requiresReasoningEcho?: boolean;
}

export interface UIOpenRouterConfig {
  baseUrl?: string;
  // The operator secret store ref the gateway credential is saved under. A ref,
  // not a value: the token lives in erun's own operator secret store, so config
  // stays safe to back up and share and nothing sensitive reaches helm argv or
  // a launch command.
  authTokenRef?: string;
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
// again.
export interface UIHostGatewayDefaults {
  baseUrl?: string;
  model?: string;
  context?: number;
}

// UIGatewayCredentialStatus is which credential a deploy would deliver, and
// where it comes from. It identifies the value rather than revealing it: the
// desktop has no use for a live token.
export interface UIGatewayCredentialStatus {
  ref: string;
  // 'saved' is a value stored in ERun settings; 'host' is this machine's own
  // Claude Code key, reused because it is already pointed at this same gateway;
  // absent means no key can be delivered and a deploy cannot authenticate. A
  // plain string because the Go side is one: the generated binding cannot carry
  // a union, so the two values are named here rather than narrowed into a type
  // the bridge would reject.
  source?: string;
  hint?: string;
  // The gateway this machine's own Claude Code is pointed at, when it is. Set
  // alongside an absent source, it is what lets the panel say the key belongs to
  // another gateway rather than claiming there is none.
  hostEndpoint?: string;
}
