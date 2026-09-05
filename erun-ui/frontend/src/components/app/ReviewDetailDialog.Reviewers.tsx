import { Button, SelectField } from 'erun-kit';
import { Copy, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { showNotification } from '@/app/notificationThunks';
import {
  cancelAddReviewer,
  cancelRemoveReviewer,
  clearAddReviewerError,
  clearRemoveReviewerError,
  confirmRemoveReviewer,
  setAddReviewerUserId,
  startAddReviewer,
  submitAddReviewer,
  submitRemoveReviewer,
} from '@/app/reviewDetailThunks';
import type { ReviewDetailState } from '@/app/state';
import type { UIReviewer } from '@/types';

import { InlineAlert, PermissionNotice } from './InlineAlert';
import { PlatformErrorAlert } from './PlatformSignInAlert';

// ReviewDetailReviewers is the desktop's Add reviewers action, alongside the
// CLI's `erun review reviewers` and MCP's `review_reviewers_*` tools.
// Reviewers gate no status transition — assigning one only makes `erun
// review list --waiting-on-me` (and this same list) reachable for that user.
export function ReviewDetailReviewers({
  data,
  detail,
}: {
  data: NonNullable<ReviewDetailState['data']>;
  detail: ReviewDetailState;
}): React.ReactElement {
  if (data.reviewersRestricted) {
    return (
      <PermissionNotice>
        You do not have access to this review&apos;s reviewers. It needs {data.reviewersRestricted}.
      </PermissionNotice>
    );
  }
  if (data.reviewersError) {
    return <InlineAlert>{data.reviewersError}</InlineAlert>;
  }
  const reviewers = data.reviewers ?? [];
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-[13px] font-medium text-foreground">Reviewers</h3>
      {reviewers.length === 0 ? (
        <p className="text-[13px] text-muted-foreground">No reviewers assigned yet.</p>
      ) : (
        <ul className="flex flex-col gap-1.5 text-[13px]">
          {reviewers.map((reviewer) => (
            <ReviewerRow key={reviewer.userId} reviewer={reviewer} data={data} detail={detail} />
          ))}
        </ul>
      )}
      <AddReviewerAction data={data} detail={detail} />
    </div>
  );
}

function ReviewerRow({
  reviewer,
  data,
  detail,
}: {
  reviewer: UIReviewer;
  data: NonNullable<ReviewDetailState['data']>;
  detail: ReviewDetailState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const label = reviewer.username ?? reviewer.userId;
  const confirming = detail.removeReviewerConfirmingUserId === reviewer.userId;
  const removing = detail.removingReviewerId === reviewer.userId;
  const hasError =
    detail.removeReviewerErrorUserId === reviewer.userId && detail.removeReviewerError;
  return (
    <li className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <span className="min-w-0 truncate text-foreground">{label}</span>
        {data.canRemoveReviewers &&
          (confirming ? (
            <span className="flex items-center gap-1.5">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={removing}
                onClick={() => {
                  dispatch(cancelRemoveReviewer());
                }}
              >
                Cancel
              </Button>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={removing}
                onClick={() => {
                  void dispatch(submitRemoveReviewer(reviewer.userId));
                }}
              >
                {removing && <LoaderCircle className="animate-spin" aria-hidden="true" />}
                Confirm remove
              </Button>
            </span>
          ) : (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="ml-auto"
              onClick={() => {
                dispatch(confirmRemoveReviewer(reviewer.userId));
              }}
            >
              Remove
            </Button>
          ))}
      </div>
      {hasError && (
        <PlatformErrorAlert
          message={detail.removeReviewerError}
          alias={detail.callerPlatformAlias}
          onRecovered={() => {
            dispatch(clearRemoveReviewerError());
          }}
        />
      )}
    </li>
  );
}

// AddReviewerAction degrades by permission (canAssignReviewers), matching
// CloseReviewAction's own shape. The picker offers only availableReviewers —
// the tenant's own enrolled users — never a free-text id, which is what
// keeps a cross-tenant userId structurally unreachable from this control.
function AddReviewerAction({
  data,
  detail,
}: {
  data: NonNullable<ReviewDetailState['data']>;
  detail: ReviewDetailState;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (!data.canAssignReviewers) {
    return null;
  }
  if (!detail.addReviewerOpen) {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="w-fit"
        onClick={() => {
          dispatch(startAddReviewer());
        }}
      >
        Add reviewer
      </Button>
    );
  }
  const assigned = new Set((data.reviewers ?? []).map((reviewer) => reviewer.userId));
  const choices = (data.availableReviewers ?? []).filter((user) => !assigned.has(user.userId));
  if (choices.length === 0) {
    return <NoReviewersLeftToAssign />;
  }
  return <AddReviewerPicker choices={choices} detail={detail} />;
}

// NoReviewersLeftToAssign is the "no dead ends" case the picker cannot show
// an empty option list as: the remedy names the exact administrator command
// and makes it copyable, rather than leaving the operator to reconstruct it.
function NoReviewersLeftToAssign(): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex flex-col gap-2">
      <PermissionNotice>
        Every enrolled user in this tenant is already a reviewer. Ask an administrator to enroll
        another with{' '}
        <code className="rounded bg-muted px-1 py-0.5 text-[12px]">erun platform user enroll</code>.
      </PermissionNotice>
      <div className="flex items-center gap-2">
        <CopyCommandButton command="erun platform user enroll --username <username> --issuer <issuer> --subject <subject>" />
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            dispatch(cancelAddReviewer());
          }}
        >
          Cancel
        </Button>
      </div>
    </div>
  );
}

function AddReviewerPicker({
  choices,
  detail,
}: {
  choices: UIReviewer[];
  detail: ReviewDetailState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-end gap-2">
        <SelectField
          id="add-reviewer-user"
          label="Reviewer"
          value={detail.addReviewerUserId}
          options={choices.map((user) => ({
            value: user.userId,
            label: user.username ?? user.userId,
          }))}
          placeholder="Choose a user"
          disabled={detail.addReviewerSubmitting}
          onChange={(userId) => {
            dispatch(setAddReviewerUserId(userId));
          }}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={detail.addReviewerSubmitting}
          onClick={() => {
            dispatch(cancelAddReviewer());
          }}
        >
          Cancel
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={detail.addReviewerSubmitting || !detail.addReviewerUserId}
          onClick={() => {
            void dispatch(submitAddReviewer());
          }}
        >
          {detail.addReviewerSubmitting && (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          )}
          Assign
        </Button>
      </div>
      {detail.addReviewerError && (
        <PlatformErrorAlert
          message={detail.addReviewerError}
          alias={detail.callerPlatformAlias}
          onRecovered={() => {
            dispatch(clearAddReviewerError());
          }}
        />
      )}
    </div>
  );
}

// CopyCommandButton mirrors TenantPlatformState.tsx's not-enrolled hand-off:
// an administrator recovering from "no one left to assign" gets the exact
// command copyable, not something to retype by hand.
function CopyCommandButton({ command }: { command: string }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-label="Copy enrollment command"
      onClick={() => {
        void navigator.clipboard.writeText(command).then(() => {
          dispatch(showNotification('success', 'Copied the enrollment command.'));
        });
      }}
    >
      <Copy aria-hidden="true" />
      Copy command
    </Button>
  );
}
