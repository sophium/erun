import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';
import { captureInvokes } from '../../../pages/index.js';

// OSC 52 writes reach the host clipboard; reads are never answered, since
// handing the host clipboard to a pod process is a trust decision.

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
    // The write drains the same parse queue, so once visible the read was parsed.
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
    // The drain marker proves the oversized sequence was parsed and dropped.
    await app.terminalPane.emitOutput(sessionId, osc52('c', 'drain-marker'));

    await expect.poll(() => app.terminalPane.hostClipboard()).toBe('drain-marker');
  });
});
