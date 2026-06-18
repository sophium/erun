import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// terminal-paste-file covers #584: the desktop terminal's clipboard paste used
// to accept image files only — a pasted PDF / CSV / archive was silently
// dropped by the image-only MIME filter. After the fix, ANY pasted file is
// copied into the runtime pod and its remote path typed into the shell, with
// the original filename preserved.
//
// Harness limitation: the actual copy is `kubectl exec ... base64 -d > path`
// into a live runtime pod, which the headless harness deliberately lacks
// (kubectl is an inert stub). So this spec mocks the SavePastedFile RPC over
// /__erun_invoke and locks the two reachable frontend invariants:
//   1. a non-image paste dispatches SavePastedFile carrying the file's real
//      MIME type and name — proving the old image-only gate is gone; and
//   2. the remote path SavePastedFile returns is typed into the terminal via
//      SendSessionInput.
// The copy itself, the filename derivation, and the path-traversal safety are
// covered by the Go tests in erun-ui/app_test.go (TestSavePastedFile*,
// TestPastedFileFilenameDerivation, TestPastedFileFilenameRejectsTraversal).

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

// dispatchFilePaste fires a synthetic `paste` ClipboardEvent on the terminal
// root carrying one file, mirroring the browser paste the controller listens
// for. The handler reads event.clipboardData.items, so a DataTransfer with the
// file added drives the exact production path.
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
        // Stand in for the live `kubectl exec` copy the harness can't perform.
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

    // Open a seeded env so a terminal session exists (the paste handler is a
    // no-op until store.terminal.sessionId > 0).
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Re-dispatch until the session is established and the handler fires. Each
    // dispatch is an idempotent synthetic event; before the session is ready it
    // returns early without an RPC, so the poll closes the open→session race
    // without a brittle fixed wait.
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

    // The original filename is preserved (an agent sees report.pdf, not
    // paste-….bin), and the payload carries the file bytes.
    expect(savePayload?.name).toBe('report.pdf');
    expect(typeof savePayload?.data).toBe('string');
    expect(savePayload?.data?.length ?? 0).toBeGreaterThan(0);

    // The remote path the copy returned is typed into the terminal.
    await expect
      .poll(() => sentInputs.some((input) => input.includes(REMOTE_PATH)), { timeout: 5_000 })
      .toBe(true);
  });
});
