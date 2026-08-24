import { ListChecks } from 'lucide-react';
import * as React from 'react';

import { useActivityQueue } from '@/app/activityQueueState';
import { ActivityQueueDrawer } from '@/components/app/ActivityQueueDrawer';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

// The drawer stays mounted even while closed so its open/close CSS
// transition can slide it in and out.
export function ActivityQueueLauncher({
  open,
  onOpen,
  onClose,
}: {
  open: boolean;
  onOpen: () => void;
  onClose: () => void;
}): React.ReactElement {
  const { entries } = useActivityQueue();
  const activeCount = entries.filter((entry) => entry.status === 'running').length;
  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size="icon"
            className="fixed bottom-5 right-5 z-20 size-10 rounded-full shadow-lg"
            aria-label={
              activeCount > 0
                ? `Open activities (${String(activeCount)} active)`
                : 'Open activities'
            }
            onClick={onOpen}
          >
            <ListChecks aria-hidden="true" className="size-4" />
            {activeCount > 0 && (
              <span className="absolute -top-1 -right-1 inline-flex size-4 items-center justify-center rounded-full bg-blue-500 text-[10px] font-medium text-white">
                {activeCount}
              </span>
            )}
          </Button>
        </TooltipTrigger>
        <TooltipContent side="left">
          {activeCount > 0
            ? `${String(activeCount)} activit${activeCount > 1 ? 'ies' : 'y'} in progress`
            : 'Activities'}
        </TooltipContent>
      </Tooltip>
      <ActivityQueueDrawer open={open} onClose={onClose} />
    </>
  );
}
