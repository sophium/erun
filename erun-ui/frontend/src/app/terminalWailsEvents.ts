import type { TerminalExitPayload, TerminalOutputPayload } from '@/types';

import { EventsOn } from '../../wailsjs/runtime/runtime';
import { reloadStateAfterEnvironmentChange } from './bootThunks';
import type {
  AIActivityPayload,
  AppNotificationPayload,
  AppStatusPayload,
  DoctorCompletedPayload,
  EnvActivityPayload,
  EnvironmentInitializedPayload,
  EnvStatusPayload,
  EnvUsagePayload,
  OrchestratorShellActivityPayload,
  SSHDInitCompletedPayload,
} from './model';
import { store } from './store';
import {
  handleAIActivity,
  handleAppNotification,
  handleAppStatus,
  handleDoctorCompleted,
  handleEnvActivity,
  handleEnvironmentDeployed,
  handleEnvironmentInitFailed,
  handleEnvironmentInitialized,
  handleEnvStatus,
  handleEnvUsage,
  handleOrchestratorShellActivity,
  handleReconnectLine,
  handleSSHDInitCompleted,
  handleTerminalExit,
} from './wailsEventThunks';

// Owns the desktop's Wails event subscriptions as one lifecycle: they are
// registered together and detached together, so a listener cannot be added
// without also being torn down on unmount.
export class TerminalWailsEvents {
  private offs: (() => void)[] = [];

  subscribe(onTerminalOutput: (payload: TerminalOutputPayload) => void): void {
    this.offs = [
      EventsOn('terminal-output', onTerminalOutput),
      EventsOn('terminal-exit', (payload: TerminalExitPayload) => {
        store.dispatch(handleTerminalExit(payload));
      }),
      EventsOn('app-status', (payload: AppStatusPayload) => {
        store.dispatch(handleAppStatus(payload));
      }),
      EventsOn('app-notification', (payload: AppNotificationPayload) => {
        store.dispatch(handleAppNotification(payload));
      }),
      EventsOn('mcp-reconnect-line', (line: string) => {
        store.dispatch(handleReconnectLine(line));
      }),
      EventsOn('environments-changed', () => {
        void store.dispatch(reloadStateAfterEnvironmentChange());
      }),
      EventsOn('ai-activity', (payload: AIActivityPayload) => {
        store.dispatch(handleAIActivity(payload));
      }),
      EventsOn('orchestrator-shell-activity', (payload: OrchestratorShellActivityPayload) => {
        store.dispatch(handleOrchestratorShellActivity(payload));
      }),
      EventsOn('env-status', (payload: EnvStatusPayload) => {
        store.dispatch(handleEnvStatus(payload));
      }),
      EventsOn('env-activity', (payload: EnvActivityPayload) => {
        store.dispatch(handleEnvActivity(payload));
      }),
      EventsOn('env-usage', (payload: EnvUsagePayload) => {
        store.dispatch(handleEnvUsage(payload));
      }),
      EventsOn('doctor-completed', (payload: DoctorCompletedPayload) => {
        store.dispatch(handleDoctorCompleted(payload));
      }),
      EventsOn('sshd-init-completed', (payload: SSHDInitCompletedPayload) => {
        store.dispatch(handleSSHDInitCompleted(payload));
      }),
      ...this.environmentLifecycleSubscriptions(),
    ];
  }

  detach(): void {
    for (const off of this.offs) {
      off();
    }
    this.offs = [];
  }

  private environmentLifecycleSubscriptions(): (() => void)[] {
    return [
      EventsOn('environment-initialized', (payload: EnvironmentInitializedPayload) => {
        void store.dispatch(handleEnvironmentInitialized(payload));
      }),
      EventsOn('environment-init-failed', (payload: EnvironmentInitializedPayload) => {
        store.dispatch(handleEnvironmentInitFailed(payload));
      }),
      EventsOn('environment-deployed', (payload: EnvironmentInitializedPayload) => {
        void store.dispatch(handleEnvironmentDeployed(payload));
      }),
    ];
  }
}
