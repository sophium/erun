import { GitFork } from 'lucide-react';
import * as React from 'react';

import { toggleContribute } from '@/app/contributeThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { contributeEnvKey } from '@/app/slices/contributeSlice';
import { IconTooltip } from '@/components/app/IconTooltip';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

const titlebarButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

const activeTitlebarButtonClassName =
  'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

// ContributeToggle is the titlebar Contribute switch. It is rendered
// only for non-erun envs whose configured type is local-agent or
// remote-agent. Toggling on triggers a clone of the ERun source repo
// inside the env and spawns the two contribute tabs; toggling off
// closes those tabs.
export function ContributeToggle(): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const selected = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const flag = useAppSelector((state) => {
    if (!selected) return false;
    const key = contributeEnvKey(selected.tenant, selected.environment);
    return Boolean(state.contribute.flagsByEnv[key]);
  });
  const [busy, setBusy] = React.useState(false);

  if (!selected) return null;
  if (selected.tenant.toLowerCase() === 'erun') return null;

  const tenant = tenants.find((t) => t.name === selected.tenant);
  const env = tenant?.environments.find((e) => e.name === selected.environment);
  if (!env) return null;
  const type = (env.type ?? '').toLowerCase();
  if (type !== 'local-agent' && type !== 'remote-agent') return null;

  const label = flag ? 'Disable contribute mode' : 'Contribute to ERun';

  return (
    <IconTooltip label={label}>
      <Button
        className={cn(titlebarButtonClassName, flag && activeTitlebarButtonClassName)}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        aria-pressed={flag}
        disabled={busy}
        onClick={() => {
          if (busy) return;
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
