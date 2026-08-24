import { Button } from 'erun-kit';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { openPinVersion } from '@/app/pinVersionThunks';
import type { UISelection } from '@/types';

// Deploying a version and pinning one are different motions: deploy rolls the
// running runtime, pin realigns every place the repo records the version. They
// sit together because an operator reaching for one often means the other.
export function PinVersionAction({
  selection,
  disabled,
}: {
  selection: UISelection | null;
  disabled: boolean;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (!selection) {
    return null;
  }
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border border-border/60 px-3 py-2">
      <div className="min-w-0">
        <div className="text-sm font-medium">erun version pins</div>
        <div className="text-[11px] text-muted-foreground">
          Re-pin the Terraform refs, chart dependencies and build-env image together.
        </div>
      </div>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={disabled}
        aria-label={`Change erun version for ${selection.tenant} / ${selection.environment}`}
        onClick={() => {
          void dispatch(openPinVersion(selection));
        }}
      >
        Change erun version
      </Button>
    </div>
  );
}
