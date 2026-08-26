import { Button, IconTooltip } from 'erun-kit';
import { GitPullRequestCreate } from 'lucide-react';
import * as React from 'react';

import { openCreateReviewDialog } from '@/app/createReviewDialogThunks';
import { useAppDispatch } from '@/app/hooks';

// StartReviewFromDiffAction is the diff panel's own entry point into the
// "Open a review" dialog: the panel already knows the environment and the
// branch it is diffing against, so opening the dialog from here carries both
// instead of sending the operator to the Reviews tab to re-specify what they
// were already looking at. It composes the existing dialog and its thunks
// (openCreateReviewDialog) rather than duplicating them — the dialog itself
// resolves whether this caller may create a review at all (Restricted state),
// so this button always renders, the same way DiffLineCommentAction's comment
// affordance always renders and explains a blocked precondition on click
// rather than disappearing pre-emptively.
export function StartReviewFromDiffAction({
  tenant,
  environment,
  targetBranch,
}: {
  tenant: string;
  environment: string;
  targetBranch: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label={`Start a review from ${tenant} / ${environment}`}>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          void dispatch(openCreateReviewDialog({ tenant, environment, targetBranch }));
        }}
      >
        <GitPullRequestCreate aria-hidden="true" />
        Start a review
      </Button>
    </IconTooltip>
  );
}
