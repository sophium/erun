import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
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

// CreateReviewDialog opens a review from the desktop (#1348). Push is the
// precondition of create — the platform can only reference a sourceBranch
// that already exists on the remote — so both steps live in one dialog with
// their own explicit buttons and their own busy/error state, rather than one
// combined action the operator cannot see the pieces of.
export function CreateReviewDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.createReviewDialog);
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
            Push your branch, then open a review proposing it merge into a target branch.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <PushBranchStep dialog={dialog} />
          <ReviewDetailsStep dialog={dialog} />
        </div>
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
            disabled={!canCreate(dialog)}
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

function canCreate(dialog: CreateReviewDialogState): boolean {
  const sourceBranch = (dialog.pushedBranch || dialog.sourceBranch).trim();
  return (
    Boolean(dialog.name.trim()) &&
    Boolean(dialog.targetBranch.trim()) &&
    Boolean(sourceBranch) &&
    !dialog.creating &&
    !dialog.committing &&
    !dialog.pushing
  );
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
        <p className="text-[13px] text-destructive">
          {dialog.branchError || 'The current branch could not be read.'}
        </p>
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
    <>
      <Input
        aria-label="Commit message"
        placeholder="Describe what changed (optional if already committed)"
        value={dialog.commitMessage}
        disabled={dialog.committing || dialog.pushing}
        onChange={(event) => {
          dispatch(updateCreateReviewDialog({ commitMessage: event.target.value }));
        }}
      />
      {dialog.commitError && <p className="text-[13px] text-destructive">{dialog.commitError}</p>}
      {dialog.pushError && <p className="text-[13px] text-destructive">{dialog.pushError}</p>}
    </>
  );
}

function PushBranchActions({ dialog }: { dialog: CreateReviewDialogState }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex items-center gap-2">
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

function ReviewDetailsStep({ dialog }: { dialog: CreateReviewDialogState }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <StepShell label="Review details">
      <Label className="grid gap-1.5 text-[13px]">
        Name
        <Input
          aria-label="Review name"
          placeholder="The eventual squash-merge message"
          value={dialog.name}
          disabled={dialog.creating}
          onChange={(event) => {
            dispatch(updateCreateReviewDialog({ name: event.target.value }));
          }}
        />
      </Label>
      <Label className="grid gap-1.5 text-[13px]">
        Target branch
        <Input
          aria-label="Target branch"
          value={dialog.targetBranch}
          disabled={dialog.creating}
          onChange={(event) => {
            dispatch(updateCreateReviewDialog({ targetBranch: event.target.value }));
          }}
        />
      </Label>
      {dialog.createError && <p className="text-[13px] text-destructive">{dialog.createError}</p>}
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
