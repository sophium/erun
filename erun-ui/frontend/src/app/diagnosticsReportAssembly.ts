import * as React from 'react';

import { stateApi } from '@/app/api/stateApi';
import type { DiagnosticsLinkedEnv, DiagnosticsReportContext } from '@/app/diagnosticsReport';
import { formatDiagnosticsReport } from '@/app/diagnosticsReport';
import { useAppSelector } from '@/app/hooks';
import type { DiagnosticsContext } from '@/app/selectors';
import type { OrchestratorInfo } from '@/app/slices/orchestratorsSlice';
import { uiTraceEntries } from '@/app/uiTraceBuffer';
import { selectionKey } from '@/app/versionSuggestions';
import type { UIBuildDetails } from '@/types';

import { LoadAppLog, LoadEnvTrace } from '../../wailsjs/go/main/App';

// diagnosticsReportAssembly turns a DiagnosticsContext into the read-only
// evidence formatDiagnosticsReport needs. It is the one place the Diagnostics
// console's Copy report and Report an erun issue actions both read from, so
// the two can never drift into carrying different evidence for the same
// context.

// useLinkedEnvironments reads the real-status label for each of an
// orchestrator's linked environments, so its report and title reflect an
// actual failure rather than only the orchestrator's own running/stopped bit.
export function useLinkedEnvironments(
  orchestrator: OrchestratorInfo | null,
): DiagnosticsLinkedEnv[] {
  return useAppSelector((state) => {
    if (!orchestrator) {
      return [];
    }
    return orchestrator.environments.map((env) => ({
      tenant: env.tenant,
      environment: env.environment,
      status: state.envStatus.statusByEnv[selectionKey(env)] ?? '',
    }));
  });
}

export function useDiagnosticsReportAssembly(context: DiagnosticsContext): {
  build: UIBuildDetails | null;
  assemble: () => Promise<{ report: string; reportContext: DiagnosticsReportContext }>;
} {
  const selectInitialState = React.useMemo(
    () => stateApi.endpoints.getInitialState.select(undefined),
    [],
  );
  const build = useAppSelector((state) => selectInitialState(state).data?.build ?? null);
  const environment = useAppSelector((state) => {
    if (context.kind !== 'environment') {
      return null;
    }
    const tenant = state.tenants.tenants.find((entry) => entry.name === context.tenant);
    return tenant?.environments.find((env) => env.name === context.environment) ?? null;
  });
  const envStatus = useAppSelector((state) =>
    context.kind === 'environment'
      ? (state.envStatus.statusByEnv[
          selectionKey({ tenant: context.tenant, environment: context.environment })
        ] ?? '')
      : '',
  );
  const linkedEnvironments = useLinkedEnvironments(
    context.kind === 'orchestrator' ? context.orchestrator : null,
  );

  const assemble = React.useCallback(async (): Promise<{
    report: string;
    reportContext: DiagnosticsReportContext;
  }> => {
    let reportContext: DiagnosticsReportContext;
    if (context.kind === 'environment') {
      const trace = await LoadEnvTrace({
        tenant: context.tenant,
        environment: context.environment,
      }).catch(() => null);
      reportContext = {
        kind: 'environment',
        tenant: context.tenant,
        environment: context.environment,
        env: environment,
        status: envStatus,
        trace,
      };
    } else if (context.kind === 'orchestrator') {
      const appLog = await LoadAppLog().catch(() => null);
      reportContext = {
        kind: 'orchestrator',
        orchestrator: context.orchestrator,
        linkedEnvironments,
        appLog,
      };
    } else {
      const appLog = await LoadAppLog().catch(() => null);
      reportContext = { kind: 'app', appLog };
    }
    const report = formatDiagnosticsReport({
      generatedAt: new Date().toISOString(),
      build,
      context: reportContext,
      uiTrace: uiTraceEntries(),
    });
    return { report, reportContext };
  }, [context, environment, envStatus, linkedEnvironments, build]);

  return { build, assemble };
}
