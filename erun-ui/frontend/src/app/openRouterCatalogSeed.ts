import type { UIERunConfig } from '@/types';
import type {
  UIHostGatewayDefaults,
  UIOpenRouterConfig,
  UIOpenRouterModel,
} from '@/uiOpenRouterTypes';

type SeedableDefaults = UIHostGatewayDefaults & { baseUrl: string };

// seedOpenRouterCatalog pre-fills from this machine's Claude Code gateway; it
// never overwrites a configured catalog and stores nothing until save.
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

// A catalog with models but no base URL is unfinished, not configured.
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

// No model in the settings seeds none, rather than a blank row.
function seededModels(defaults: UIHostGatewayDefaults): UIOpenRouterModel[] {
  if (!defaults.model) {
    return [];
  }
  return [{ id: defaults.model, context: defaults.context }];
}
