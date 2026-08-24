import { Button, cn } from 'erun-kit';
import {
  AlertTriangle,
  CheckCircle2,
  HelpCircle,
  LoaderCircle,
  Rocket,
  Stethoscope,
} from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  checkManageEnvironmentHealth,
  deployFromHealthCheck,
  focusRegistryFieldFromHealthCheck,
} from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import type { UIEnvironmentHealthCheck } from '@/uiDiagnosticsTypes';

type ManageDialog = AppState['manageDialog'];

// EnvironmentHealthSection runs the out-of-pod "Check environment" diagnostics
// (effective container registry configured, runtime deployed) and renders each
// result as a first-class line with a recovery action. It is an explicit user
// action — the dialog never runs it implicitly on open (Nielsen: user control).
export function EnvironmentHealthSection({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const health = useAppSelector((state) => state.manageDialog.health);
  const loading = useAppSelector((state) => state.manageDialog.healthLoading);
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
            Environment health
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={dialog.busy || dialog.configLoading || loading}
          onClick={() =>
            void dispatch(checkManageEnvironmentHealth()).catch((error: unknown) => {
              dispatch(showTerminalMessage(readError(error)));
            })
          }
        >
          {loading ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <Stethoscope aria-hidden="true" />
          )}
          {loading ? 'Checking...' : 'Check environment'}
        </Button>
      </div>
      {!health && !loading && (
        <p className="text-xs text-muted-foreground">
          Checks that this environment has a container registry and that its runtime is deployed.
        </p>
      )}
      {health && (
        <div className="grid gap-2" role="status" aria-live="polite">
          {health.healthy && <HealthyBanner />}
          {health.checks.map((check) => (
            <HealthCheckRow key={check.id} check={check} disabled={dialog.busy} />
          ))}
        </div>
      )}
    </div>
  );
}

function HealthyBanner(): React.ReactElement {
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-2 rounded-[var(--radius)] border border-green-600/35 bg-green-600/10 px-3 py-2 text-[13px] leading-[1.4]">
      <CheckCircle2 className="size-4 text-green-600 dark:text-green-500" aria-hidden="true" />
      <span className="font-medium">All checks passed.</span>
    </div>
  );
}

// Non-color-only status: each state has a distinct icon shape (WCAG 1.4.1).
function HealthCheckRow({
  check,
  disabled,
}: {
  check: UIEnvironmentHealthCheck;
  disabled?: boolean;
}): React.ReactElement {
  const tone = healthTone(check.status);
  return (
    <div
      role={tone === 'error' ? 'alert' : 'status'}
      className={cn(
        'grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 rounded-[var(--radius)] border px-3 py-2 text-[13px] leading-[1.4]',
        toneClassName(tone),
      )}
    >
      <HealthCheckIcon tone={tone} />
      <span className="min-w-0 [overflow-wrap:anywhere]">
        <span className="font-medium">{check.title}</span>
        <span className="text-muted-foreground"> — {check.detail}</span>
      </span>
      <HealthCheckFix fix={check.fix} disabled={disabled} />
    </div>
  );
}

function HealthCheckFix({
  fix,
  disabled,
}: {
  fix?: string;
  disabled?: boolean;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (fix === 'deploy') {
    return (
      <Button
        type="button"
        size="sm"
        className="shrink-0"
        disabled={disabled}
        onClick={() =>
          void dispatch(deployFromHealthCheck()).catch((error: unknown) => {
            dispatch(showTerminalMessage(readError(error)));
          })
        }
      >
        <Rocket aria-hidden="true" />
        Deploy
      </Button>
    );
  }
  if (fix === 'set-registry') {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="shrink-0"
        disabled={disabled}
        onClick={() => {
          dispatch(focusRegistryFieldFromHealthCheck());
        }}
      >
        Set registry
      </Button>
    );
  }
  return null;
}

type HealthTone = 'ok' | 'error' | 'unknown';

function healthTone(status: string): HealthTone {
  if (status === 'ok') {
    return 'ok';
  }
  if (status === 'error') {
    return 'error';
  }
  return 'unknown';
}

function toneClassName(tone: HealthTone): string {
  switch (tone) {
    case 'ok':
      return 'border-green-600/35 bg-green-600/10';
    case 'error':
      return 'border-destructive/40 bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)]';
    case 'unknown':
      return 'border-amber-500/40 bg-amber-500/10';
  }
}

function HealthCheckIcon({ tone }: { tone: HealthTone }): React.ReactElement {
  if (tone === 'ok') {
    return (
      <CheckCircle2
        className="size-4 shrink-0 text-green-600 dark:text-green-500"
        aria-hidden="true"
      />
    );
  }
  if (tone === 'error') {
    return <AlertTriangle className="size-4 shrink-0 text-destructive" aria-hidden="true" />;
  }
  return (
    <HelpCircle className="size-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
  );
}
