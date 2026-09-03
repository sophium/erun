import { expect, test } from '../fixtures/erunApp.js';

// erun:events-dropped is the headless HTTP+SSE bridge's reserved gap marker
// (erun-ui/headlessserver/server.go's eventsDroppedName): a full per-tab
// event buffer replaces its oldest queued event with this one instead of
// discarding a real event silently, so a tab that fell behind can tell "I
// missed something" apart from "nothing happened". Before this spec, nothing
// in the frontend listened for the marker -- it was queued, occupied a slot,
// and was discarded by the dispatcher's exact-name lookup, so the tab it
// exists to protect kept rendering whatever its last-received event left
// behind with no visible sign anything was wrong. This drives the exact
// reserved event through the real SSE transport -- the same
// window.runtime.EventsEmit path app-notification.spec.ts uses for the
// analogous `app-notification` event -- and asserts the frontend both warns
// visibly and resyncs its state from the backend, never staying quietly
// stale. The channel-eviction arithmetic that produces this exact marker
// under real backpressure is proven separately, server-side, by
// TestEmitReplacesDroppedEventWithGapMarker in
// erun-ui/headlessserver/server_test.go (a full 64-slot channel cannot be
// forced deterministically from a browser tab, since draining depends on the
// server's own goroutine scheduling, not anything the page can observe or
// control) -- this spec is what closes the loop once that marker reaches the
// wire, so asserting only that the marker was queued can never again pass as
// proof the frontend actually consumes it.

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
    // A resync means a fresh LoadState round-trip (the same call
    // reloadStateAfterEnvironmentChange makes), not the one boot() already
    // made before this test body runs -- wait for the next one specifically,
    // so an unrelated already-settled request can't be mistaken for it.
    const resync = page.waitForResponse(
      (response) =>
        response.url().endsWith('/__erun_invoke') &&
        (response.request().postDataJSON() as { method?: string } | null)?.method === 'LoadState',
    );

    await emitEventsDropped(page, 7);

    const icon = app.titlebar.messageCenterIcon('warning');
    await expect(icon).toBeVisible();
    await icon.click();
    await expect(app.titlebar.messageCenterRow(/Lost 7 updates from the app/)).toBeVisible();

    const response = await resync;
    expect(response.ok()).toBe(true);
  });
});
