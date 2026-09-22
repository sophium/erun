import type { UIERunConfig } from '@/types';
import type {
  UIHostGatewayDefaults,
  UIOpenRouterConfig,
  UIOpenRouterModel,
} from '@/uiOpenRouterTypes';

// A host gateway with a base URL, offered as the catalog's starting point.
type SeedableDefaults = UIHostGatewayDefaults & { baseUrl: string };

// seedOpenRouterCatalog offers this machine's own Claude Code gateway as the
// catalog's starting point, so an operator who already runs Claude Code through
// a gateway does not retype an endpoint and model they configured once.
//
// It fills the dialog only. Nothing is stored until the operator saves, and a
// catalog they have already configured is never overwritten: the defaults are
// a starting point, not a source of truth.
export function seedOpenRouterCatalog(config: UIERunConfig): UIERunConfig {
  const defaults = config.openRouterDefaults;
  if (!defaults?.baseUrl || isConfigured(config)) {
    return config;
  }
  return {
    ...config,
    openRouter: seededCatalog(config, { ...defaults, baseUrl: defaults.baseUrl }),
  };
}

// isConfigured reports whether the operator has already named a gateway. Only
// the base URL decides it: a catalog holding models but no gateway is one they
// have begun and not finished.
function isConfigured(config: UIERunConfig): boolean {
  return (config.openRouter?.baseUrl ?? '').trim() !== '';
}

function seededCatalog(config: UIERunConfig, defaults: SeedableDefaults): UIOpenRouterConfig {
  const existing = config.openRouter?.models ?? [];
  return {
    ...config.openRouter,
    baseUrl: defaults.baseUrl,
    defaultModel: config.openRouter?.defaultModel ?? defaults.model,
    models: existing.length > 0 ? existing : seededModels(defaults),
  };
}

// seededModels offers the model those settings run on, with the window they
// declare for it. Settings that name no model seed none: the operator picks
// from the gateway's published list instead of being handed a blank row.
function seededModels(defaults: UIHostGatewayDefaults): UIOpenRouterModel[] {
  if (!defaults.model) {
    return [];
  }
  return [{ id: defaults.model, context: defaults.context }];
}
