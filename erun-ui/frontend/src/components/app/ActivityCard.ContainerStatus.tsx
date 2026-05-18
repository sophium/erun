import { ChevronRight } from 'lucide-react';
import * as React from 'react';

import type { ActivityQueueContainerStatus, ActivityQueueEntry } from '@/app/activityQueueState';
import {
  containerIsFailing,
  containerPhaseClassName,
  containerPhaseDotClassName,
  containerPhaseLabel,
  copyToClipboard,
  isRecoverableContainerFailure,
  kubectlDescribeCommand,
} from '@/components/app/ActivityQueueDrawer.helpers';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

import { StartForceDeploySession } from '../../../wailsjs/go/main/App';
import type { main as wailsMain } from '../../../wailsjs/go/models';

export function ContainerStatusList({
  containers,
  deploy,
}: {
  containers: ActivityQueueContainerStatus[];
  deploy: ActivityQueueEntry;
}): React.ReactElement {
  return (
    <ul className="mt-2 space-y-1">
      {containers.map((container) => (
        <li key={container.name}>
          <ContainerStatusRow container={container} deploy={deploy} />
        </li>
      ))}
    </ul>
  );
}

function ContainerPhaseIndicator({
  container,
}: {
  container: ActivityQueueContainerStatus;
}): React.ReactElement {
  return (
    <span
      className={cn(
        'flex flex-none items-center gap-1 text-[10px] uppercase tracking-wider',
        containerPhaseClassName(container),
      )}
    >
      <span
        aria-hidden="true"
        className={cn('inline-block size-1.5 rounded-full', containerPhaseDotClassName(container))}
      />
      {containerPhaseLabel(container)}
      {container.restarts > 0 && (
        <span className="text-muted-foreground">
          · {container.restarts} restart{container.restarts > 1 ? 's' : ''}
        </span>
      )}
    </span>
  );
}

function ContainerStatusRow({
  container,
  deploy,
}: {
  container: ActivityQueueContainerStatus;
  deploy: ActivityQueueEntry;
}): React.ReactElement {
  const failing = containerIsFailing(container);
  const [expanded, setExpanded] = React.useState<boolean>(failing);
  // Failing containers default to expanded so the user sees the cause
  // without an extra click; user can still collapse and re-expand.
  React.useEffect(() => {
    setExpanded(failing);
  }, [failing]);

  const hasDetails =
    container.image !== '' || (container.message ?? '') !== '' || (container.reason ?? '') !== '';
  return (
    <div
      className={cn(
        'rounded-sm border border-transparent bg-muted/40 px-2 py-1',
        failing && 'border-destructive/30',
      )}
    >
      <button
        type="button"
        className="flex w-full items-center justify-between gap-2 text-left text-[11px] outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
        aria-expanded={expanded}
        aria-controls={`container-detail-${container.name}`}
        disabled={!hasDetails}
        onClick={() => {
          setExpanded((value) => !value);
        }}
      >
        <span className="flex items-center gap-1 truncate font-medium">
          {hasDetails && (
            <ChevronRight
              aria-hidden="true"
              className={cn(
                'size-3 transition-transform text-muted-foreground',
                expanded && 'rotate-90',
              )}
            />
          )}
          <span className="truncate">{container.name}</span>
        </span>
        <ContainerPhaseIndicator container={container} />
      </button>
      {expanded && hasDetails && (
        <ContainerStatusDetail
          id={`container-detail-${container.name}`}
          container={container}
          deploy={deploy}
        />
      )}
    </div>
  );
}

function ContainerStatusDetail({
  id,
  container,
  deploy,
}: {
  id: string;
  container: ActivityQueueContainerStatus;
  deploy: ActivityQueueEntry;
}): React.ReactElement {
  const recovery = recoveryActionForContainer(container, deploy);
  return (
    <dl
      id={id}
      className="mt-1 grid grid-cols-[max-content_1fr] gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground"
    >
      {container.image && (
        <>
          <dt className="font-medium text-foreground">Image</dt>
          <dd className="break-all font-mono text-[10.5px]">{container.image}</dd>
        </>
      )}
      {container.reason && (
        <>
          <dt className="font-medium text-foreground">Reason</dt>
          <dd
            className={cn(
              'font-mono text-[10.5px]',
              containerIsFailing(container) && 'text-destructive',
            )}
          >
            {container.reason}
          </dd>
        </>
      )}
      {container.message && (
        <>
          <dt className="font-medium text-foreground">Message</dt>
          <dd className="whitespace-pre-wrap break-words">{container.message}</dd>
        </>
      )}
      <dt className="font-medium text-foreground">Inspect</dt>
      <dd className="break-all font-mono text-[10px]">
        <code className="text-foreground">{kubectlDescribeCommand(container, deploy)}</code>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="ml-1 h-5 px-1 text-[10px]"
          onClick={() => {
            void copyToClipboard(kubectlDescribeCommand(container, deploy));
          }}
        >
          Copy
        </Button>
      </dd>
      {recovery && (
        <>
          <dt className="font-medium text-foreground">Recover</dt>
          <dd className="space-y-1">
            <p className="text-[11px] text-muted-foreground">{recovery.hint}</p>
            <Button
              type="button"
              variant="default"
              size="xs"
              className="h-6 text-[11px]"
              onClick={() => {
                void recovery.action();
              }}
            >
              {recovery.label}
            </Button>
          </dd>
        </>
      )}
    </dl>
  );
}

interface RecoveryAction {
  hint: string;
  label: string;
  action: () => Promise<void>;
}

// recoveryActionForContainer returns a one-click recovery affordance when
// the container's failure mode has a known mitigation. The most common
// case is a registry miss: the kubelet message contains "not found"
// against an image tag the chart references, which usually means the
// chart bumped the tag without publishing it. `erun deploy --force`
// rebuilds every image bypassing the fingerprint cache and pushes them
// to the registry, so the missing tag becomes available.
function recoveryActionForContainer(
  container: ActivityQueueContainerStatus,
  deploy: ActivityQueueEntry,
): RecoveryAction | null {
  if (!isRecoverableContainerFailure(container)) return null;
  const selection: wailsMain.uiSelection = {
    tenant: deploy.tenant,
    environment: deploy.environment,
    version: deploy.version ?? '',
    runtimeImage: '',
    runtimeCpu: '',
    runtimeMemory: '',
    kubernetesContext: deploy.kubernetesContext ?? '',
    containerRegistry: '',
    noGit: false,
    bootstrap: false,
    setDefaultTenant: false,
    action: 'deploy',
    debug: false,
  };
  return {
    hint: `${container.image || 'The image referenced by the chart'} is not in the registry. Rebuild every image bypassing the fingerprint cache and push them.`,
    label: 'Rebuild & redeploy',
    action: async () => {
      await StartForceDeploySession(selection, 120, 34);
    },
  };
}
