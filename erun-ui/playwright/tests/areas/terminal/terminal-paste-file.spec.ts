import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Any pasted file is copied into the pod and its path typed. The kubectl copy
// is mocked; the copy itself is Go-tested.

interface InvokeBody {
  method?: string;
  args?: unknown[];
}

interface PastePayload {
  data?: string;
  mimeType?: string;
  name?: string;
}

const REMOTE_PATH = '/home/erun/.codex/attachments/paste-20260101-000000.000000000-report.pdf';

// The paste handler reads event.clipboardData.items.
async function dispatchFilePaste(
  page: import('@playwright/test').Page,
  file: { name: string; type: string; text: string },
): Promise<boolean> {
  return page.evaluate((f) => {
    const root = document.querySelector('.terminal');
    if (!root) {
      return false;
    }
    const transfer = new DataTransfer();
    transfer.items.add(new File([f.text], f.name, { type: f.type }));
    root.dispatchEvent(
      new ClipboardEvent('paste', { clipboardData: transfer, bubbles: true, cancelable: true }),
    );
    return true;
  }, file);
}

test.describe('terminal paste of any file (#584)', () => {
  test('a pasted non-image file is copied and its path typed into the terminal', async ({
    app,
    page,
  }) => {
    let savePayload: PastePayload | undefined;
    const sentInputs: string[] = [];

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'SavePastedFile') {
        savePayload = body.args?.[1] ?? {};
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { path: REMOTE_PATH } }),
        });
      }
      if (body.method === 'SendSessionInput' && typeof body.args?.[1] === 'string') {
        sentInputs.push(body.args[1]);
      }
      await route.continue();
    });

    // The paste handler is a no-op until a session exists.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Re-dispatch until the session is ready; earlier dispatches return without an RPC.
    await expect
      .poll(
        async () => {
          await dispatchFilePaste(page, {
            name: 'report.pdf',
            type: 'application/pdf',
            text: '%PDF-1.7 body',
          });
          return savePayload?.mimeType;
        },
        { timeout: 15_000 },
      )
      .toBe('application/pdf');

    expect(savePayload?.name).toBe('report.pdf');
    expect(typeof savePayload?.data).toBe('string');
    expect(savePayload?.data?.length ?? 0).toBeGreaterThan(0);

    await expect.poll(() => sentInputs.some((input) => input.includes(REMOTE_PATH))).toBe(true);
  });
});
