import { Button, cn, IconTooltip } from 'erun-kit';
import { ExternalLink, GitFork } from 'lucide-react';
import * as React from 'react';

import { toggleContribute } from '@/app/contributeThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { showNotification } from '@/app/notificationThunks';
import { contributeEnvKey } from '@/app/slices/contributeSlice';
import type { UISelection } from '@/types';

import { StartContributeApp } from '../../../wailsjs/go/main/App';

const titlebarButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const activeTitlebarButtonClassName =
  'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

// Contribute mode clones ERun into an agent env so an operator can work on
// ERun itself from inside the running pod; the launcher then serves that
// in-pod app to the browser. Both need the pod up, so while the cloud env is
// down the buttons stay rendered — to preserve titlebar layout — but disabled.
export function ContributeToggle({
  envRunning,
}: {
  envRunning: boolean;
}): React.ReactElement | null {
  const selected = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  if (!selected) return null;
  if (selected.tenant.toLowerCase() === 'erun') return null;
  const tenant = tenants.find((t) => t.name === selected.tenant);
  const env = tenant?.environments.find((e) => e.name === selected.environment);
  if (!env) return null;
  const type = (env.type ?? '').toLowerCase();
  if (type !== 'local-agent' && type !== 'remote-agent') return null;
  return <ContributeToggleControls selected={selected} envRunning={envRunning} />;
}

function ContributeToggleControls({
  selected,
  envRunning,
}: {
  selected: UISelection;
  envRunning: boolean;
}): React.ReactElement {
  const flag = useAppSelector((state) =>
    Boolean(state.contribute.flagsByEnv[contributeEnvKey(selected.tenant, selected.environment)]),
  );
  return (
    <>
      <ContributeToggleButton selected={selected} flag={flag} envRunning={envRunning} />
      {flag && <ContributeAppLauncher selected={selected} envRunning={envRunning} />}
    </>
  );
}

function ContributeToggleButton({
  selected,
  flag,
  envRunning,
}: {
  selected: UISelection;
  flag: boolean;
  envRunning: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const [busy, setBusy] = React.useState(false);
  const baseLabel = flag ? 'Disable contribute mode' : 'Contribute to ERun';
  const label = envRunning ? baseLabel : `${baseLabel} — start the cloud environment first`;
  const disabled = busy || !envRunning;
  return (
    <IconTooltip label={label}>
      <Button
        className={cn(titlebarButtonClassName, flag && activeTitlebarButtonClassName)}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        aria-pressed={flag}
        disabled={disabled}
        onClick={() => {
          if (disabled) return;
          setBusy(true);
          void dispatch(toggleContribute(selected)).finally(() => {
            setBusy(false);
          });
        }}
      >
        <GitFork />
      </Button>
    </IconTooltip>
  );
}

function ContributeAppLauncher({
  selected,
  envRunning,
}: {
  selected: UISelection;
  envRunning: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const [busy, setBusy] = React.useState(false);
  const baseLabel = 'Open contribute app in browser';
  const label = envRunning ? baseLabel : `${baseLabel} — start the cloud environment first`;
  const disabled = busy || !envRunning;
  return (
    <IconTooltip label={label}>
      <Button
        className={titlebarButtonClassName}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        disabled={disabled}
        onClick={() => {
          if (disabled) return;
          setBusy(true);
          // Surface progress immediately: the first build can take ~3
          // minutes, and without feedback the button just sits disabled
          // with no sign of life.
          dispatch(
            showNotification(
              'info',
              'Building contribute app in env — first build can take 2–3 minutes; watch the ERun (contribute) tab for progress.',
            ),
          );
          StartContributeApp(selected)
            .then((launch) => {
              if (launch.url) {
                dispatch(showNotification('success', `Contribute app ready at ${launch.url}`));
              }
            })
            .catch((e: unknown) => {
              const message = e instanceof Error ? e.message : String(e);
              dispatch(showNotification('error', `Failed to open contribute app: ${message}`));
            })
            .finally(() => {
              setBusy(false);
            });
        }}
      >
        <ExternalLink />
      </Button>
    </IconTooltip>
  );
}
