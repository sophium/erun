import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  StatusBadge,
  TabsContent,
  Textarea,
} from 'erun-kit';
import { Check, Inbox, LoaderCircle, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { inviteRequestStatusTone } from '@/app/tenantDashboardPanels';
import {
  approveInviteRequest,
  cancelDecliningInviteRequest,
  confirmDeclineInviteRequest,
  setDeclineReasonDraft,
  startDecliningInviteRequest,
} from '@/app/tenantInviteRequestThunks';
import {
  INVITE_REQUEST_KIND_CREATE_TENANT,
  type UIInviteRequest,
  type UITenantDashboard,
} from '@/types';

import { ClipboardSetText } from '../../../wailsjs/runtime/runtime';
import { InlineAlert, PermissionNotice } from './InlineAlert';
import {
  DataCell,
  DataTable,
  PanelBody,
  RelativeTime,
  type TenantDashboardData,
} from './TenantDashboardMessage';

// TenantDashboardPanels.Requests.tsx is the operator/admin queue for
// invite-requests (issue #1682 §3): who is waiting, what they asked for, and
// the Issue invitation / Decline actions. A pending count belongs on the tab
// itself (RequestsTabLabel), visible without opening this panel — the
// issue's own named failure mode is "an unattended queue nobody sees."
export function RequestsPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const requests = data?.inviteRequests ?? [];
  return (
    <TabsContent value="requests" className="min-h-0 overflow-auto">
      <PanelBody
        data={data}
        tab="requests"
        empty={
          <EmptyState
            icon={<Inbox />}
            heading="No pending requests"
            body="Requests to join or register a tenant will appear here as they arrive."
          />
        }
      >
        {requests.length > 0 && <RequestsTable data={data} requests={requests} />}
      </PanelBody>
      <IssuedInviteLinkNotice />
    </TabsContent>
  );
}

function RequestsTable({
  data,
  requests,
}: {
  data: UITenantDashboard | null | undefined;
  requests: UIInviteRequest[];
}): React.ReactElement {
  const canApprove = data?.canApproveInviteRequests ?? false;
  const canDecline = data?.canDeclineInviteRequests ?? false;
  return (
    <>
      <DataTable
        headers={['Requester', 'Asking for', 'Note', 'Waited', 'Status', 'Actions']}
        columnWidths={['w-[18%]', 'w-[20%]', 'w-[26%]', 'w-[10%]', 'w-[10%]', 'w-[16%]']}
      >
        {requests.map((request) => (
          <RequestRow
            key={request.inviteRequestId}
            request={request}
            canApprove={canApprove}
            canDecline={canDecline}
          />
        ))}
      </DataTable>
      {!canApprove && !canDecline && (
        <div className="mt-3">
          <PermissionNotice>
            You can see this queue, but issuing invitations or declining requests needs additional
            access. Ask an administrator.
          </PermissionNotice>
        </div>
      )}
    </>
  );
}

function RequestRow({
  request,
  canApprove,
  canDecline,
}: {
  request: UIInviteRequest;
  canApprove: boolean;
  canDecline: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const decidingId = useAppSelector((state) => state.tenantDashboard.decidingInviteRequestId);
  const decideError = useAppSelector((state) => state.tenantDashboard.decideInviteRequestError);
  const busy = decidingId === request.inviteRequestId;
  return (
    <tr>
      <DataCell strong>
        <span className="block truncate font-mono text-[12px]" title={request.issuer}>
          {request.subject}
        </span>
      </DataCell>
      <DataCell>
        {request.kind === INVITE_REQUEST_KIND_CREATE_TENANT ? 'Register' : 'Join'}{' '}
        {request.tenantName}
        {request.environmentName ? ` / ${request.environmentName}` : ''}
      </DataCell>
      <DataCell>{request.note}</DataCell>
      <DataCell>
        <RelativeTime value={request.createdAt} />
      </DataCell>
      <DataCell>
        <StatusBadge tone={inviteRequestStatusTone(request.status)} label={request.status} />
      </DataCell>
      <DataCell>
        <div className="flex flex-col items-start gap-1">
          <div className="flex gap-1.5">
            {canApprove && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => {
                  void dispatch(approveInviteRequest(request.inviteRequestId));
                }}
              >
                {busy ? (
                  <LoaderCircle className="animate-spin" aria-hidden="true" />
                ) : (
                  <Check aria-hidden="true" />
                )}
                Issue invitation
              </Button>
            )}
            {canDecline && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => {
                  dispatch(startDecliningInviteRequest(request.inviteRequestId));
                }}
              >
                <X aria-hidden="true" />
                Decline
              </Button>
            )}
          </div>
          {busy && decideError && <InlineAlert>{decideError}</InlineAlert>}
        </div>
      </DataCell>
      <DeclineInviteRequestDialog inviteRequestId={request.inviteRequestId} />
    </tr>
  );
}

// DeclineInviteRequestDialog requires a non-empty reason before the
// destructive confirm enables — a decline with no reason reaches nobody, and
// root AGENTS.md forbids that dead end. Mirrors DeactivateUserDialog's
// outline-Cancel/destructive-confirm/spinner-while-busy shape.
function DeclineInviteRequestDialog({
  inviteRequestId,
}: {
  inviteRequestId: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const decliningId = useAppSelector((state) => state.tenantDashboard.decliningInviteRequestId);
  const decidingId = useAppSelector((state) => state.tenantDashboard.decidingInviteRequestId);
  const reason = useAppSelector((state) => state.tenantDashboard.declineReasonDraft);
  const open = decliningId === inviteRequestId;
  const busy = decidingId === inviteRequestId;
  if (!open) {
    return <></>;
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(cancelDecliningInviteRequest());
        }
      }}
    >
      <DialogContent aria-labelledby="decline-invite-request-heading">
        <DialogHeader>
          <DialogTitle id="decline-invite-request-heading">Decline this request?</DialogTitle>
          <DialogDescription>
            The requester will see this reason on their own status check. A decline needs a reason —
            there is no way for them to learn why otherwise.
          </DialogDescription>
        </DialogHeader>
        <Textarea
          rows={3}
          placeholder="Why is this request being declined?"
          value={reason}
          disabled={busy}
          onChange={(event) => {
            dispatch(setDeclineReasonDraft(event.target.value));
          }}
        />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => {
              dispatch(cancelDecliningInviteRequest());
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={busy || !reason.trim()}
            onClick={() => {
              void dispatch(confirmDeclineInviteRequest());
            }}
          >
            {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
            Decline
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// IssuedInviteLinkNotice keeps the just-minted invite link on screen after
// Issue invitation succeeds — the transferable artefact and manual fallback
// (issue #1682 §6.4) — since the approved row itself disappears from the
// pending list the moment the queue refetches.
function IssuedInviteLinkNotice(): React.ReactElement | null {
  const link = useAppSelector((state) => state.tenantDashboard.issuedInviteLink);
  if (!link) {
    return null;
  }
  return (
    <div
      className="mt-3 flex items-center justify-between gap-2 rounded-[var(--radius)] border border-border bg-muted/30 px-3 py-2 text-[13px]"
      role="status"
    >
      <span className="min-w-0 truncate font-mono" title={link.token}>
        Invitation issued: {link.token}
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          void ClipboardSetText(link.token);
        }}
      >
        Copy
      </Button>
    </div>
  );
}
