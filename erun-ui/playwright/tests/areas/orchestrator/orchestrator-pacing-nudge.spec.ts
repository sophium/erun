import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT, removeOrchestrator } from '../../../fixtures/seedRoot.js';

// #1260: the desktop re-states the orchestrator pacing contract into a live
// session every 10 minutes, and relaunches a session that died from a crash
// (never one the operator cleanly quit or Stopped) into the same conversation.
//
// Neither half of that has a reachable surface in this harness. Both text-into-
// the-pty and the crash-relaunch require a REAL orchestrator PTY: the pacing
// nudge only fires against a session whose activity report has gone stale over
// ten real minutes, and the respawn only fires when that PTY's underlying
// process actually exits non-zero. Driving either for real means launching a
// real `claude` process via StartOrchestrator — which this suite must not do:
// every orchestrator the desktop starts runs on the one shared agent account
// (erun-ui/AGENTS.md, "Spawning an AI agent is a resource decision"), and
// unlike the headless harness's intended host, an environment that already has
// `claude` on PATH (as this one does) would spawn a REAL nested session rather
// than failing the LookPath preflight closed.
//
// So this spec only drives the safe half: creating an orchestrator (which
// never starts a session — CreateOrchestrator "creates stopped") and asserting
// it renders as stopped with none of the reconciler's side effects, which is
// the negative invariant that IS reachable. The nudge decision, the cap/rearm
// bookkeeping, the crash-vs-clean-exit distinction, and the same-conversation
// respawn are covered by the Go suite instead:
// erun-ui/orchestrator_pacing_test.go (TestDecideOrchestratorPacing,
// TestReconcileOrchestratorPacingNudgesAQuietSession,
// TestOrchestratorPacingCapsAfterRepeatedSilenceAndRearmsOnFreshBusy,
// TestSendSessionInputRearmsOrchestratorPacing) and erun-ui/orchestrator_test.go
// (TestOrchestratorRespawnsAfterCrashIntoTheSameConversation,
// TestOrchestratorCleanExitDoesNotRespawn, TestStopOrchestratorRefusesItsOwnRespawn,
// TestBuildOrchestratorLaunchFallbackKeepsThePin).
//
// #1699's env-aware suppression (a linked environment busy on this
// orchestrator's own dispatched work must not accrue staleness or take a
// nudge) is the same unreachable-without-a-real-PTY case, so it is covered the
// same way: erun-ui/orchestrator_pacing_env_test.go
// (TestOrchestratorPacingSuppressedWhileLinkedEnvBusy,
// TestOrchestratorPacingBusySuppressionOnlyCountsLinkedEnvironments,
// TestOrchestratorPacingBusySuppressionOnlyCountsThisOrchestratorsLease,
// TestOrchestratorPacingUnknownEnvActivityFallsBackToBaseSignal,
// TestOrchestratorPacingSurfacesPastTheEnvBusyBound,
// TestOrchestratorPacingBusySuppressionDoesNotConsumeTheCap,
// TestOrchestratorLinkedEnvBusyStateForDiscriminatesByHolder).
test.describe('orchestrator pacing/respawn has no surface without a live session', () => {
  test('a created-but-never-started orchestrator stays stopped with no busy/shell/alert artifacts', async ({
    app,
  }) => {
    const name = 'pacing-nudge-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await expect(app.sidebar.orchestratorStatusDot(name, 'stopped')).toBeVisible();
      await expect(app.sidebar.orchestratorBusySpinner(name)).toHaveCount(0);
      await expect(app.sidebar.orchestratorShellSpinner(name)).toHaveCount(0);
      await expect(app.sidebar.orchestratorsAlert()).toHaveCount(0);
    } finally {
      removeOrchestrator(name);
    }
  });
});
