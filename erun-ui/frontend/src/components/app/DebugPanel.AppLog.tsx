import * as React from 'react';

import { useLinkedEnvironments } from '@/app/diagnosticsReportAssembly';
import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';
import type { UIAppLog } from '@/types';

import { LoadAppLog } from '../../../wailsjs/go/main/App';
import { useStickToBottom } from './DebugPanel.hooks';
import { PrimaryPaneToolbar } from './DebugPanel.shared';

const APP_LOG_POLL_MS = 2000;

function useAppLogPoll(): { appLog: UIAppLog | null; refresh: () => void } {
  const [appLog, setAppLog] = React.useState<UIAppLog | null>(null);
  const refresh = React.useCallback(() => {
    void LoadAppLog()
      .then((next) => {
        setAppLog(next);
      })
      .catch(() => {
        setAppLog(null);
      });
  }, []);
  React.useEffect(() => {
    refresh();
    const timer = window.setInterval(refresh, APP_LOG_POLL_MS);
    return () => {
      window.clearInterval(timer);
    };
  }, [refresh]);
  return { appLog, refresh };
}

// AppLogPane is the desktop's own bounded log tail — evidence for the App
// context, and the tail of the orchestrator context's own pane below it.
export function AppLogPane({ label }: { label: string }): React.ReactElement {
  const { appLog, refresh } = useAppLogPoll();
  const content = appLog?.available ? (appLog.content ?? '') : '';
  const { outputRef, handleScroll } = useStickToBottom(content);
  return (
    <div className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
      <PrimaryPaneToolbar label={appLog?.path ?? ''} content={content} onRefresh={refresh} />
      <div
        ref={outputRef}
        onScroll={handleScroll}
        className="min-h-0 overflow-auto px-3 pb-2 font-mono text-[11px] leading-[1.35] text-[oklch(0.82_0_0)]"
        aria-label={`${label} output`}
      >
        {appLog ? (
          appLog.available ? (
            <pre className="m-0 font-mono text-[11px] leading-[1.35] break-words whitespace-pre-wrap">
              {content}
            </pre>
          ) : (
            <p className="m-0 text-[oklch(0.6_0_0)]">{appLog.reason ?? 'No log available.'}</p>
          )
        ) : null}
      </div>
    </div>
  );
}

// OrchestratorPane is the orchestrator context: its identity and linked
// environments up top (the evidence an env trace can't carry — an
// orchestrator has no selection to load one for), the desktop log below,
// since a session or shell fault is desktop-side, not env-side.
export function OrchestratorPane({
  orchestrator,
}: {
  orchestrator: OrchestratorInfo;
}): React.ReactElement {
  const linkedEnvironments = useLinkedEnvironments(orchestrator);
  return (
    <div
      className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]"
      aria-label="orchestrator diagnostics"
    >
      <div className="border-b border-[oklch(0.14_0_0)] px-3 py-1.5 text-[11px] text-[oklch(0.76_0_0)]">
        <p className="m-0">
          <span className="font-medium">{orchestrator.name}</span> — {orchestrator.status}
          {orchestrator.busy ? ', busy' : ''}
          {orchestrator.shellRunning
            ? `, background shell: ${orchestrator.shellCommand || '(command unknown)'}`
            : ''}
        </p>
        <ul className="m-0 mt-1 list-none p-0 text-[oklch(0.6_0_0)]">
          {linkedEnvironments.length === 0 ? (
            <li>no linked environments</li>
          ) : (
            linkedEnvironments.map((env) => (
              <li key={`${env.tenant}/${env.environment}`}>
                {env.tenant} / {env.environment}
                {env.status ? ` — ${env.status}` : ''}
              </li>
            ))
          )}
        </ul>
      </div>
      <AppLogPane label="orchestrator" />
    </div>
  );
}
