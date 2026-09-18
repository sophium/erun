import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';
import { captureInvokes } from '../../../pages/index.js';

// #969, second half: an in-pod agent that prints "(c to copy)" emits OSC 52 to
// ask the terminal to put the text on the clipboard. With no handler registered
// xterm swallowed the sequence, so the text never left the pod — and the pod is
// the side with no browser to open the URL with.
//
// The write direction is honoured; the read direction (`OSC 52 ; c ; ?`) is
// deliberately never answered, because the host clipboard holds whatever the
// operator last copied and handing that to a pod-side process is a trust
// decision the copy affordance does not need.
//
// Harness limitation: the sequence is injected as session output rather than
// produced by a real agent in a real runtime pod (the headless harness has no
// cluster). Everything downstream of the bytes arriving — parse, bound, decode,
// host-clipboard write — is the production path. The payload-level guards are
// unit-covered in frontend/src/app/clipboard.test.ts.

const BEL = '\x07';

function osc52(selection: string, text: string): string {
  return `\x1b]52;${selection};${Buffer.from(text, 'utf8').toString('base64')}${BEL}`;
}

test.describe('OSC 52 clipboard writes from a session (#969)', () => {
  test('a clipboard write from the session lands on the host clipboard', async ({
    app,
    seededEnv,
  }) => {
    const url = 'https://claude.test/oauth/authorize?code_challenge=abcdef&state=ghijkl';
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-osc52');

    await app.terminalPane.emitOutput(sessionId, osc52('c', url));

    await expect.poll(() => app.terminalPane.hostClipboard()).toBe(url);
  });

  test('a read request is never answered back to the session', async ({ app, page, seededEnv }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('host-clipboard-secret-not-for-the-pod');

    const invokes = captureInvokes(page);
    await app.terminalPane.emitOutput(sessionId, `\x1b]52;c;?${BEL}`);
    // A write emitted after the read drains the same parse queue, so once its
    // effect is visible every byte of the read has been parsed too — the
    // negative below is bounded by a real event, not a guessed delay.
    await app.terminalPane.emitOutput(sessionId, osc52('c', 'drain-marker'));
    await expect.poll(() => app.terminalPane.hostClipboard()).toBe('drain-marker');

    const replies = invokes.filter(
      (call) =>
        call.method === 'SendSessionInput' &&
        typeof call.args[1] === 'string' &&
        call.args[1].includes('52;'),
    );
    expect(replies).toHaveLength(0);
  });

  test('an oversized write from the session is ignored', async ({ app, seededEnv }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-oversized');

    await app.terminalPane.emitOutput(sessionId, osc52('c', 'x'.repeat(128 * 1024)));
    // The drain marker proves the oversized sequence was parsed and dropped
    // rather than merely still in flight.
    await app.terminalPane.emitOutput(sessionId, osc52('c', 'drain-marker'));

    await expect.poll(() => app.terminalPane.hostClipboard()).toBe('drain-marker');
  });
});
