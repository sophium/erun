import { cn } from 'erun-kit';
import * as React from 'react';

import { loadDiffReviewStatus } from '@/app/diffReviewStatusThunks';
import { useAppDispatch } from '@/app/hooks';
import type { ReviewTarget } from '@/app/selectors';

import { DiffReviewAction, DiffReviewStatusChip } from './DiffList.ReviewAction';
import { ReviewTargetLabel } from './ReviewPanel.EnvLabel';

// Flattened to strings so the effect can depend on them; empty for a directory.
function reviewAnchorFields(target: ReviewTarget): {
  envKey: string;
  tenant: string;
  environment: string;
} {
  if (target.kind !== 'env') {
    return { envKey: '', tenant: '', environment: '' };
  }
  return { envKey: target.envKey, tenant: target.tenant, environment: target.environment };
}

// A directory has no tenant, so it gets a note instead of the status chip and
// review action.
export function DiffEnvSectionHeader({
  target,
  targetBranchHint,
  showHeader,
}: {
  target: ReviewTarget;
  targetBranchHint: string;
  showHeader: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const anchor = reviewAnchorFields(target);

  // Depends on the anchor's fields, not the target: the target is rebuilt every
  // render and this read dispatches, which would loop.
  React.useEffect(() => {
    if (!anchor.envKey || !targetBranchHint) {
      return;
    }
    void dispatch(
      loadDiffReviewStatus(anchor.envKey, anchor.tenant, anchor.environment, targetBranchHint),
    );
  }, [dispatch, anchor.envKey, anchor.tenant, anchor.environment, targetBranchHint]);

  // The chip and action render unconditionally so they never flicker while the diff loads.
  return (
    <div
      // Lets reviewDiffKeyboardNav find this section's "Start a review" button;
      // directory sections have none.
      data-env-key={target.envKey}
      className={cn(
        'sticky top-0 z-10 flex items-center gap-3 border-b border-border bg-background px-3 py-1',
        showHeader ? 'justify-between' : 'justify-end',
      )}
    >
      {showHeader && <ReviewTargetLabel target={target} />}
      <div className="flex min-w-0 flex-1 items-center justify-end gap-2">
        {anchor.envKey ? (
          <>
            <DiffReviewStatusChip envKey={anchor.envKey} tenant={anchor.tenant} />
            <DiffReviewAction
              tenant={anchor.tenant}
              environment={anchor.environment}
              targetBranch={targetBranchHint}
              envKey={anchor.envKey}
            />
          </>
        ) : (
          <span className="text-[11px] text-muted-foreground">
            Local directory — no hosted review
          </span>
        )}
      </div>
    </div>
  );
}
