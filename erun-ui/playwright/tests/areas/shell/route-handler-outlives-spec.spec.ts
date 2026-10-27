import type { Request, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// A method the backend does not know: it answers at once, and the desktop never calls it, so
// the only request this spec holds is its own.
const PROBE_METHOD = 'RouteHandlerTeardownProbe';

// Specs patch backend replies through route handlers, and the desktop keeps polling, so a
// handler can still be inside route.fetch() when its spec ends. This holds one there on
// purpose: the spec's own assertions all pass, so only the handler's late error could fail it.
test('a route handler still in flight when its spec ends does not fail the spec', async ({
  app,
  page,
}) => {
  let inFlight = false;
  let markSuspended: () => void = () => undefined;
  const suspended = new Promise<void>((resolve) => {
    markSuspended = resolve;
  });
  const pageClosed = new Promise<void>((resolve) => page.once('close', () => resolve()));
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method !== PROBE_METHOD) {
      await route.fallback();
      return;
    }
    const response = await route.fetch();
    inFlight = true;
    markSuspended();
    await pageClosed;
    const json = (await response.json()) as unknown;
    await route.fulfill({ response, json });
  });

  await app.page.evaluate((method) => {
    void fetch('/__erun_invoke', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method, args: [] }),
    });
  }, PROBE_METHOD);
  await suspended;
  expect(inFlight).toBe(true);
});
