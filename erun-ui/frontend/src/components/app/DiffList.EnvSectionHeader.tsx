import { cn } from 'erun-kit';
import * as React from 'react';

import { loadDiffReviewStatus } from '@/app/diffReviewStatusThunks';
import { useAppDispatch } from '@/app/hooks';
import type { ReviewTarget } from '@/app/selectors';

import { DiffReviewAction, DiffReviewStatusChip } from './DiffList.ReviewAction';
import { ReviewTargetLabel } from './ReviewPanel.EnvLabel';

// reviewAnchorFields is a target's tenant-scoped anchor: the fields a hosted
// review is resolved through. They are flattened to plain strings, empty for a
// directory, so the header can depend on them directly -- see the effect's own
// note on why depending on the target itself is not equivalent.
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

// DiffEnvSectionHeader is one target's sticky header: its label, plus the
// review-status chip and the Start-a-review action for an environment. It lives
// in its own file because DiffList.tsx is at its line budget.
//
// The review status chip and the Start-a-review action are platform reads and
// writes: each resolves a tenant-scoped client and anchors to a hosted review
// record. A directory belongs to no tenant, so for it those two are absent
// rather than rendered into a failure -- and the note that takes their place
// says which of the two situations the operator is looking at.
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

  // Resolves the chip once the target branch is known and whenever it changes --
  // a background enrichment read, not a user action, the same way the diff itself
  // auto-loads.
  //
  // The deps are the anchor's own fields, never the target itself. A target is a
  // directory-or-environment union rebuilt on every store update, so its identity
  // changes on every render, and this read has no in-flight guard: depending on
  // the object re-ran the read on every render, and every run dispatches, which
  // re-renders -- a loop that never settles while the platform is reachable. What
  // that costs is not only the wasted reads: the chip keeps resolving against
  // whatever the platform answers last, so a status that changes on its own while
  // the panel is open is skipped over instead of shown. A directory leaves the
  // key empty and the read never runs.
  React.useEffect(() => {
    if (!anchor.envKey || !targetBranchHint) {
      return;
    }
    void dispatch(
      loadDiffReviewStatus(anchor.envKey, anchor.tenant, anchor.environment, targetBranchHint),
    );
  }, [dispatch, anchor.envKey, anchor.tenant, anchor.environment, targetBranchHint]);

  // The same ReviewEnvLabel treatment the review-layers block and the
  // changed-files tree use, so all three per-environment surfaces
  // read as one group instead of three independently-labelled ones. The
  // sticky wrapper stays: it is a real functional need (this header keeps the
  // active environment identity visible while a long diff scrolls), unlike
  // the label styling it wraps.
  //
  // Unlike the label, the chip and action render unconditionally — a
  // persistent affordance per environment section rather than one that only
  // appears once files have loaded, so it never flickers in and out as the
  // diff itself loads, errors, or comes back empty.
  return (
    <div
      // data-env-key lets keyboard navigation (reviewDiffKeyboardNav's
      // startReviewForFocusedEnv) find this section's own "Start a review"
      // button without duplicating the dialog-opening logic here. A directory
      // section carries the attribute too and simply has no such button to
      // find, which is why that lookup is optional-chained.
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
