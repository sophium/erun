import { expect, test } from '../../../fixtures/erunApp.js';

// erun:events-dropped is the SSE bridge's gap marker (headlessserver eventsDroppedName); the
// frontend must warn and resync on it.
// Backpressure producing the marker is covered server-side by
// TestEmitReplacesDroppedEventWithGapMarker.

function emitEventsDropped(page: import('@playwright/test').Page, missed: number): Promise<void> {
  return page.evaluate((n) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('erun:events-dropped', n);
  }, missed);
}

test.describe('erun:events-dropped gap marker', () => {
  test('warns visibly and resyncs state instead of proceeding on stale data', async ({
    app,
    page,
  }) => {
    // Wait for the next LoadState specifically, not the one boot() already made.
    const resync = page.waitForResponse(
      (response) =>
        response.url().endsWith('/__erun_invoke') &&
        (response.request().postDataJSON() as { method?: string } | null)?.method === 'LoadState',
    );

    await emitEventsDropped(page, 7);

    const icon = app.titlebar.messageCenterIcon('warning');
    await icon.waitFor({ state: 'visible' });
    await expect(icon).toBeVisible();
    await icon.click();
    const row = app.titlebar.messageCenterRow(/Lost 7 updates from the app/);
    await row.waitFor({ state: 'visible' });
    await expect(row).toBeVisible();

    const response = await resync;
    expect(response.ok()).toBe(true);
  });
});
