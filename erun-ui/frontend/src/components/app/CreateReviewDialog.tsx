import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EditableComboField,
  FieldLabel,
  Input,
  StatusBadge,
} from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import {
  closeCreateReviewDialog,
  commitCreateReviewBranch,
  pushCreateReviewBranch,
  submitCreateReview,
  updateCreateReviewDialog,
} from '@/app/createReviewDialogThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import type { CreateReviewDialogState } from '@/app/reviewWriteState';
import { selectReviewTargetBranches } from '@/app/selectors';

import { InlineAlert } from './InlineAlert';

// CreateReviewDialog opens a review from the desktop. Push is the precondition
// of create — the platform can only reference a sourceBranch that already
// exists on the remote — so both steps live in one dialog with their own
// explicit buttons and their own busy/error state, rather than one combined
// action the operator cannot see the pieces of. Every write here lands in one
// named environment, so the dialog says which one before anything runs.
export function CreateReviewDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.createReviewDialog);
  const missing = missingToCreate(dialog);
  return (
    <Dialog
      open={dialog.open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeCreateReviewDialog());
        }
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Open a review</DialogTitle>
          <DialogDescription>
            Pushes the branch checked out in{' '}
            <span className="font-mono">{environmentLabel(dialog)}</span> and opens a review
            proposing it merge into a target branch.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <PushBranchStep dialog={dialog} />
          <ReviewDetailsStep dialog={dialog} />
        </div>
        {missing && !dialog.creating && (
          <p className="text-[13px] text-muted-foreground">{missing}</p>
        )}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={dialog.committing || dialog.pushing || dialog.creating}
            onClick={() => {
              dispatch(closeCreateReviewDialog());
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={Boolean(missing) || dialog.creating}
            onClick={() => {
              void dispatch(submitCreateReview());
            }}
          >
            {dialog.creating && <LoaderCircle className="animate-spin" aria-hidden="true" />}
            Create review
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function environmentLabel(dialog: CreateReviewDialogState): string {
  return dialog.environment ? `${dialog.tenant} / ${dialog.environment}` : dialog.tenant;
}

// missingToCreate names what the review still needs, so a create the operator
// cannot yet run says why instead of presenting a dead button (Nielsen:
// visibility of system status, error prevention).
function missingToCreate(dialog: CreateReviewDialogState): string {
  if (dialog.committing || dialog.pushing) {
    return 'Wait for the branch step to finish.';
  }
  if (!(dialog.pushedBranch || dialog.sourceBranch).trim()) {
    return 'This needs a branch to propose — the environment’s current branch could not be read.';
  }
  const absent = [
    dialog.name.trim() ? '' : 'a name',
    dialog.targetBranch.trim() ? '' : 'a target branch',
  ].filter(Boolean);
  return absent.length > 0 ? `Add ${absent.join(' and ')} to open the review.` : '';
}

function PushBranchStep({ dialog }: { dialog: CreateReviewDialogState }): React.ReactElement {
  if (dialog.branchLoading) {
    return (
      <StepShell label="Push your branch">
        <p className="flex items-center gap-2 text-[13px] text-muted-foreground">
          <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
          Reading the current branch…
        </p>
      </StepShell>
    );
  }
  if (dialog.branchError || !dialog.sourceBranch) {
    return (
      <StepShell label="Push your branch">
        <InlineAlert>{dialog.branchError || 'The current branch could not be read.'}</InlineAlert>
      </StepShell>
    );
  }
  return (
    <StepShell label="Push your branch">
      <p className="text-[13px] text-muted-foreground">
        Current branch <span className="font-mono text-foreground">{dialog.sourceBranch}</span>
      </p>
      <PushBranchComposer dialog={dialog} />
      <PushBranchActions dialog={dialog} />
    </StepShell>
  );
}

function PushBranchComposer({ dialog }: { dialog: CreateReviewDialogState }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="grid gap-2">
      <FieldLabel htmlFor="create-review-commit-message">Commit message</FieldLabel>
      <Input
        id="create-review-commit-message"
        placeholder="Describe what changed (optional if already committed)"
        value={dialog.commitMessage}
        disabled={dialog.committing || dialog.pushing}
        onChange={(event) => {
          dispatch(updateCreateReviewDialog({ commitMessage: event.target.value }));
        }}
      />
      <p className="text-[13px] text-muted-foreground">
        Commit records every change in this environment’s worktree. Skip it if the branch is already
        committed.
      </p>
      {dialog.commitError && <InlineAlert>{dialog.commitError}</InlineAlert>}
      {dialog.pushError && <InlineAlert>{dialog.pushError}</InlineAlert>}
    </div>
  );
}

function PushBranchActions({ dialog }: { dialog: CreateReviewDialogState }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={dialog.committing || dialog.pushing || !dialog.commitMessage.trim()}
        onClick={() => {
          void dispatch(commitCreateReviewBranch());
        }}
      >
        {dialog.committing && <LoaderCircle className="animate-spin" aria-hidden="true" />}
        Commit
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={dialog.committing || dialog.pushing}
        onClick={() => {
          void dispatch(pushCreateReviewBranch());
        }}
      >
        {dialog.pushing && <LoaderCircle className="animate-spin" aria-hidden="true" />}
        Push
      </Button>
      {dialog.pushedBranch && (
        <StatusBadge tone="success" label={`Pushed to origin/${dialog.pushedBranch}`} />
      )}
    </div>
  );
}

// ReviewDetailsStep asks only for what the operator authors. The target branch
// is a known set — the branches this tenant's reviews and queue already target
// — so it is offered as choices that stay typeable for a branch that is new
// here (recognition over recall).
function ReviewDetailsStep({ dialog }: { dialog: CreateReviewDialogState }): React.ReactElement {
  const dispatch = useAppDispatch();
  const targetBranches = useAppSelector(selectReviewTargetBranches);
  return (
    <StepShell label="Review details">
      <div className="grid gap-2">
        <FieldLabel htmlFor="create-review-name" required>
          Review name
        </FieldLabel>
        <Input
          id="create-review-name"
          placeholder="The eventual squash-merge message"
          value={dialog.name}
          disabled={dialog.creating}
          onChange={(event) => {
            dispatch(updateCreateReviewDialog({ name: event.target.value }));
          }}
        />
      </div>
      <EditableComboField
        id="create-review-target-branch"
        label="Target branch"
        value={dialog.targetBranch}
        suggestions={targetBranches}
        required
        disabled={dialog.creating}
        onValueChange={(next) => {
          dispatch(updateCreateReviewDialog({ targetBranch: next }));
        }}
      />
      {dialog.createError && <InlineAlert>{dialog.createError}</InlineAlert>}
    </StepShell>
  );
}

function StepShell({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-border p-3">
      <span className="text-[13px] font-semibold text-foreground">{label}</span>
      {children}
    </div>
  );
}
