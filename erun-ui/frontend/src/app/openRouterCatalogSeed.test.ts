import { describe, expect, it } from 'vitest';

import { seedOpenRouterCatalog } from '@/app/openRouterCatalogSeed';
import type { UIERunConfig } from '@/types';

const base: UIERunConfig = { defaultTenant: 'erun' };

describe('seedOpenRouterCatalog', () => {
  it('fills an unconfigured catalog from the host gateway', () => {
    const got = seedOpenRouterCatalog({
      ...base,
      openRouterDefaults: {
        baseUrl: 'https://openrouter.ai/api',
        model: 'deepseek/deepseek-v4.1-flash',
        context: 1048576,
      },
    });
    expect(got.openRouter).toEqual({
      baseUrl: 'https://openrouter.ai/api',
      defaultModel: 'deepseek/deepseek-v4.1-flash',
      models: [{ id: 'deepseek/deepseek-v4.1-flash', context: 1048576 }],
    });
  });

  it('never overwrites a catalog the operator already configured', () => {
    // The defaults are a starting point, not a source of truth: a configured
    // catalog is theirs and must survive.
    const configured: UIERunConfig = {
      ...base,
      openRouter: {
        baseUrl: 'https://gw.example.com/anthropic',
        defaultModel: 'vendor/chosen',
        models: [{ id: 'vendor/chosen', context: 200000 }],
      },
      openRouterDefaults: { baseUrl: 'https://openrouter.ai/api', model: 'host/model' },
    };
    expect(seedOpenRouterCatalog(configured)).toEqual(configured);
  });

  it('offers nothing when the host has no gateway', () => {
    // A machine that has not configured one has nothing to offer, which is the
    // ordinary case rather than a reason to seed an empty catalog.
    for (const config of [
      base,
      { ...base, openRouterDefaults: {} },
      { ...base, openRouterDefaults: { model: 'a/b' } },
    ]) {
      expect(seedOpenRouterCatalog(config).openRouter).toBeUndefined();
    }
  });

  it('seeds the gateway with no model list when the settings name no model', () => {
    // Better a gateway with an empty list than a blank row the launch drops.
    const got = seedOpenRouterCatalog({
      ...base,
      openRouterDefaults: { baseUrl: 'https://openrouter.ai/api' },
    });
    expect(got.openRouter?.baseUrl).toBe('https://openrouter.ai/api');
    expect(got.openRouter?.models).toEqual([]);
    expect(got.openRouter?.defaultModel).toBeUndefined();
  });

  it('leaves an existing model list alone when the gateway is unset', () => {
    // Only the base URL decides whether the catalog counts as configured.
    const got = seedOpenRouterCatalog({
      ...base,
      openRouter: { models: [{ id: 'vendor/kept', context: 100 }] },
      openRouterDefaults: { baseUrl: 'https://openrouter.ai/api', model: 'host/model' },
    });
    expect(got.openRouter?.models).toEqual([{ id: 'vendor/kept', context: 100 }]);
    expect(got.openRouter?.defaultModel).toBe('host/model');
  });
});
