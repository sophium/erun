import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  FieldLabel,
  Tabs,
  TabsList,
  TabsTrigger,
  Textarea,
} from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  closeRequestInvitationDialog,
  requestInvitationSubmitDisabledReason,
  setRequestKindDraft,
  setRequestNoteDraft,
  submitInviteRequest,
} from '@/app/tenantInviteRequestThunks';
import {
  INVITE_REQUEST_KIND_CREATE_TENANT,
  INVITE_REQUEST_KIND_JOIN_TENANT,
  type UITenantDashboard,
} from '@/types';

import { InlineAlert } from './InlineAlert';

// RequestInvitationDialog is the "Request an invitation" action from
// NotEnrolledState (issue #1682 §2): the requester's identity (issuer/
// subject) is verified from the token already in hand and is never a form
// field (recognition over recall, root AGENTS.md's onboarding rule); the
// tenant/environment names are the local ones already known, shown read-only
// rather than re-typed. The only genuine choices are the request's kind
// (a known 2-option set, so a segmented Tabs control rather than free text
// or a dropdown — Professional UX "known option sets must use selectors")
// and an optional note.
export function RequestInvitationDialog({
  data,
  localEnvironmentName,
}: {
  data: UITenantDashboard;
  localEnvironmentName: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const open = useAppSelector((state) => state.tenantDashboard.requestDialogOpen);
  const requesting = useAppSelector((state) => state.tenantDashboard.requesting);
  const requestError = useAppSelector((state) => state.tenantDashboard.requestError);
  const requestKindDraft = useAppSelector((state) => state.tenantDashboard.requestKindDraft);
  const requestNoteDraft = useAppSelector((state) => state.tenantDashboard.requestNoteDraft);
  const rateLimitedUntil = useAppSelector((state) => state.tenantDashboard.requestRateLimitedUntil);
  const [now, setNow] = React.useState(() => Date.now());

  // Ticks once a second only while a rate-limit countdown is actually
  // showing, so the disabled reason's remaining-seconds text stays live
  // (Nielsen #1) without a permanent per-second re-render cost the rest of
  // the time.
  React.useEffect(() => {
    if (!open || rateLimitedUntil <= Date.now()) {
      return;
    }
    const id = window.setInterval(() => {
      setNow(Date.now());
    }, 1000);
    return () => {
      window.clearInterval(id);
    };
  }, [open, rateLimitedUntil]);

  if (!open) {
    return <></>;
  }

  const disabledReason = requestInvitationSubmitDisabledReason(requesting, rateLimitedUntil, now);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeRequestInvitationDialog());
        }
      }}
    >
      <DialogContent aria-labelledby="request-invitation-heading">
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void dispatch(
              submitInviteRequest({
                tenantName: data.tenant,
                environmentName: localEnvironmentName,
              }),
            );
          }}
        >
          <DialogHeader>
            <DialogTitle id="request-invitation-heading">Request an invitation</DialogTitle>
            <DialogDescription>
              Ask a platform operator to host {data.tenant}
              {localEnvironmentName ? ` / ${localEnvironmentName}` : ''}. Your verified identity
              goes with the request — you never type it in.
            </DialogDescription>
          </DialogHeader>
          <RequestInvitationFields
            data={data}
            requesting={requesting}
            requestError={requestError}
            requestKindDraft={requestKindDraft}
            requestNoteDraft={requestNoteDraft}
          />
          <RequestInvitationFooter requesting={requesting} disabledReason={disabledReason} />
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RequestInvitationFields({
  data,
  requesting,
  requestError,
  requestKindDraft,
  requestNoteDraft,
}: {
  data: UITenantDashboard;
  requesting: boolean;
  requestError: string;
  requestKindDraft: string;
  requestNoteDraft: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="grid gap-4 py-2">
      <IdentitySummary data={data} />
      <div className="grid gap-2">
        <FieldLabel htmlFor="request-kind-join">What are you asking for?</FieldLabel>
        <Tabs
          value={requestKindDraft}
          onValueChange={(value) => {
            dispatch(setRequestKindDraft(value));
          }}
        >
          <TabsList className="w-full">
            <TabsTrigger
              id="request-kind-join"
              value={INVITE_REQUEST_KIND_JOIN_TENANT}
              className="flex-1"
              disabled={requesting}
            >
              Join an existing tenant
            </TabsTrigger>
            <TabsTrigger
              value={INVITE_REQUEST_KIND_CREATE_TENANT}
              className="flex-1"
              disabled={requesting}
            >
              Register a new tenant
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <p className="text-[12px] leading-[1.4] text-muted-foreground">
          {requestKindDraft === INVITE_REQUEST_KIND_CREATE_TENANT
            ? `An operator will register "${data.tenant}" as a brand-new tenant with your identity as its first user.`
            : `An operator who already manages "${data.tenant}" on the platform will enroll you into it.`}
        </p>
      </div>
      <div className="grid gap-2">
        <FieldLabel htmlFor="request-note">Note to the operator (optional)</FieldLabel>
        <Textarea
          id="request-note"
          rows={3}
          placeholder="Anything the operator should know (optional)"
          value={requestNoteDraft}
          disabled={requesting}
          onChange={(event) => {
            dispatch(setRequestNoteDraft(event.target.value));
          }}
        />
      </div>
      {requestError && <InlineAlert>{requestError}</InlineAlert>}
    </div>
  );
}

function RequestInvitationFooter({
  requesting,
  disabledReason,
}: {
  requesting: boolean;
  disabledReason: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const disabled = disabledReason !== '' || requesting;
  return (
    <DialogFooter className="items-center sm:justify-between">
      <p
        id="request-invitation-submit-reason"
        className="text-left text-[12px] leading-[1.35] text-muted-foreground [overflow-wrap:anywhere] sm:max-w-[60%]"
        role="status"
        aria-live="polite"
      >
        {disabledReason}
      </p>
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={requesting}
          onClick={() => {
            dispatch(closeRequestInvitationDialog());
          }}
        >
          Cancel
        </Button>
        <Button
          type="submit"
          size="sm"
          disabled={disabled}
          aria-describedby={disabledReason ? 'request-invitation-submit-reason' : undefined}
        >
          {requesting && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          {requesting ? 'Sending…' : 'Send request'}
        </Button>
      </div>
    </DialogFooter>
  );
}

function IdentitySummary({ data }: { data: UITenantDashboard }): React.ReactElement {
  return (
    <div className="grid gap-1 rounded-[var(--radius)] border border-border bg-muted/30 p-3 text-[12px]">
      <div className="text-muted-foreground">Your verified identity</div>
      <div className="grid gap-0.5 font-mono">
        <div>Issuer: {data.platformIssuer ?? '-'}</div>
        <div>Subject: {data.platformSubject ?? '-'}</div>
      </div>
    </div>
  );
}
