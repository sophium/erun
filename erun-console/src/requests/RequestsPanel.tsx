import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  FieldLabel,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Textarea,
} from 'erun-kit';
import { Inbox, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import type { InviteRequest } from '../app/api/requestsApi';
import type { PlatformCapability } from '../app/api/whoamiApi';
import { useGetWhoamiQuery } from '../app/api/whoamiApi';
import { capabilityAllows } from '../app/capabilities';
import { queryErrorMessage } from '../app/queryError';
import type { RequestActionState, RequestsState } from './controller';
import { useRequestsController } from './controller';
import { RateLimitPanel } from './RateLimitPanel';

// The canonical route templates approve/decline gate on -- exact strings
// erun-backend-api registers them under (internal/routes/invite_requests.go),
// matched via capabilityAllows's wildcard-segment comparison.
const APPROVE_PATH_TEMPLATE = '/v1/invite-requests/{invite_request_id}/approve';
const DECLINE_PATH_TEMPLATE = '/v1/invite-requests/{invite_request_id}/decline';

function kindLabel(request: InviteRequest): string {
  return request.kind === 'CREATE_TENANT'
    ? `Register ${request.tenantName}`
    : `Join ${request.tenantName}`;
}

// formatWaited is a small relative-time helper -- no new dependency needed
// for the granularity this queue needs (minutes/hours/days).
function formatWaited(createdAt: string): string {
  const created = new Date(createdAt).getTime();
  if (Number.isNaN(created)) {
    return '—';
  }
  const minutes = Math.floor((Date.now() - created) / 60000);
  if (minutes < 1) {
    return 'just now';
  }
  if (minutes < 60) {
    return `${String(minutes)}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${String(hours)}h ago`;
  }
  return `${String(Math.floor(hours / 24))}d ago`;
}

function acceptURL(token: string): string {
  return `${window.location.origin}/accept-invite?token=${encodeURIComponent(token)}`;
}

// IssuedInviteDialog shows the minted invite link once, right after
// approval -- the same "shown once, copyable, dismiss clears it" shape
// InvitesPanel's InviteLinkDialog and UsersPanel's TemporaryPasswordDialog
// already use for a one-time credential. It has to be a dialog rather than
// an inline row note: the approved row disappears from the pending list the
// moment the mutation's own tag invalidation refetches it, so there is no
// row left for an inline note to sit beside.
function IssuedInviteDialog({
  request,
  onDismiss,
}: {
  request: InviteRequest;
  onDismiss: () => void;
}): React.ReactElement {
  const [copied, setCopied] = React.useState(false);
  const link = acceptURL(request.mintedInviteToken ?? '');
  const requesterLabel = request.displayName ?? request.email ?? request.subject;

  const copy = (): void => {
    void navigator.clipboard.writeText(link).then(() => {
      setCopied(true);
    });
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) {
          onDismiss();
        }
      }}
    >
      <DialogContent aria-labelledby="invite-issued-heading">
        <DialogHeader>
          <DialogTitle id="invite-issued-heading">Invitation issued</DialogTitle>
          <DialogDescription>
            Hand this link to {requesterLabel}
            {request.mintedInviteExpiresAt !== undefined
              ? ` — it expires ${new Date(request.mintedInviteExpiresAt).toLocaleString()} and can only be used once.`
              : '.'}
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <Input
            readOnly
            value={link}
            aria-label="Invitation link"
            className="font-mono"
            onFocus={(e) => {
              e.target.select();
            }}
          />
          <Button type="button" variant="outline" onClick={copy}>
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </div>
        <DialogFooter>
          <Button type="button" onClick={onDismiss}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// DeclineRequestDialog mirrors UsersPanel's DeactivateUserDialog shape
// exactly (outline Cancel first, destructive confirm second, both disabled
// while busy) but gates the confirm button on a non-empty reason too: a
// decline with no reason is a dead end the requester can't act on.
function DeclineRequestDialog({
  request,
  busy,
  onCancel,
  onConfirm,
}: {
  request: InviteRequest;
  busy: boolean;
  onCancel: () => void;
  onConfirm: (reason: string) => void;
}): React.ReactElement {
  const [reason, setReason] = React.useState('');
  const trimmed = reason.trim();

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) {
          onCancel();
        }
      }}
    >
      <DialogContent aria-labelledby="decline-request-heading">
        <DialogHeader>
          <DialogTitle id="decline-request-heading">
            Decline the request from {request.subject}?
          </DialogTitle>
          <DialogDescription>
            The reason is sent back to the requester — a decline with no reason is a dead end.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <FieldLabel htmlFor="decline-reason" required>
            Reason
          </FieldLabel>
          <Textarea
            id="decline-reason"
            value={reason}
            disabled={busy}
            onChange={(e) => {
              setReason(e.target.value);
            }}
          />
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={busy || trimmed === ''}
            onClick={() => {
              onConfirm(trimmed);
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

function RequesterCell({ request }: { request: InviteRequest }): React.ReactElement {
  const label = request.displayName ?? request.email;
  return (
    <div className="grid gap-0.5">
      {label !== undefined && <span className="font-medium text-foreground">{label}</span>}
      <span
        className={
          label !== undefined ? 'text-xs text-muted-foreground' : 'font-medium text-foreground'
        }
      >
        {request.issuer} · {request.subject}
      </span>
    </div>
  );
}

function RequestActions({
  request,
  actionState,
  canApprove,
  canDecline,
  onApprove,
  onRequestDecline,
}: {
  request: InviteRequest;
  actionState: RequestActionState;
  canApprove: boolean;
  canDecline: boolean;
  onApprove: (id: string) => void;
  onRequestDecline: (request: InviteRequest) => void;
}): React.ReactElement {
  const busy = actionState.status === 'busy';
  return (
    <div className="grid gap-1">
      <div className="flex gap-2">
        {canApprove && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={busy}
            onClick={() => {
              onApprove(request.inviteRequestId);
            }}
          >
            {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
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
              onRequestDecline(request);
            }}
          >
            Decline
          </Button>
        )}
      </div>
      {actionState.status === 'error' && (
        <p className="text-xs text-destructive" role="alert">
          {actionState.message}
        </p>
      )}
    </div>
  );
}

function RequestRow({
  request,
  actionState,
  canApprove,
  canDecline,
  onApprove,
  onRequestDecline,
}: {
  request: InviteRequest;
  actionState: RequestActionState;
  canApprove: boolean;
  canDecline: boolean;
  onApprove: (id: string) => void;
  onRequestDecline: (request: InviteRequest) => void;
}): React.ReactElement {
  return (
    <TableRow>
      <TableCell>
        <RequesterCell request={request} />
      </TableCell>
      <TableCell className="font-medium text-foreground">{kindLabel(request)}</TableCell>
      <TableCell className="max-w-xs whitespace-normal break-words align-top">
        {request.note ?? <span className="text-muted-foreground">—</span>}
      </TableCell>
      <TableCell>{formatWaited(request.createdAt)}</TableCell>
      <TableCell>
        <RequestActions
          request={request}
          actionState={actionState}
          canApprove={canApprove}
          canDecline={canDecline}
          onApprove={onApprove}
          onRequestDecline={onRequestDecline}
        />
      </TableCell>
    </TableRow>
  );
}

function RequestsTable({
  requests,
  actionStates,
  canApprove,
  canDecline,
  onApprove,
  onRequestDecline,
}: {
  requests: InviteRequest[];
  actionStates: Record<string, RequestActionState>;
  canApprove: boolean;
  canDecline: boolean;
  onApprove: (id: string) => void;
  onRequestDecline: (request: InviteRequest) => void;
}): React.ReactElement {
  if (requests.length === 0) {
    return <EmptyState icon={<Inbox />} heading="No pending requests." />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Requester</TableHead>
          <TableHead>Request</TableHead>
          <TableHead>Note</TableHead>
          <TableHead>Waited</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {requests.map((request) => (
          <RequestRow
            key={request.inviteRequestId}
            request={request}
            actionState={actionStates[request.inviteRequestId] ?? { status: 'idle' }}
            canApprove={canApprove}
            canDecline={canDecline}
            onApprove={onApprove}
            onRequestDecline={onRequestDecline}
          />
        ))}
      </TableBody>
    </Table>
  );
}

function RequestsBody({
  requestsState,
  actionStates,
  canApprove,
  canDecline,
  onApprove,
  onRequestDecline,
}: {
  requestsState: RequestsState;
  actionStates: Record<string, RequestActionState>;
  canApprove: boolean;
  canDecline: boolean;
  onApprove: (id: string) => void;
  onRequestDecline: (request: InviteRequest) => void;
}): React.ReactElement {
  if (requestsState.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading requests…
      </p>
    );
  }
  if (requestsState.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load requests: {requestsState.message}
      </p>
    );
  }
  return (
    <RequestsTable
      requests={requestsState.requests}
      actionStates={actionStates}
      canApprove={canApprove}
      canDecline={canDecline}
      onApprove={onApprove}
      onRequestDecline={onRequestDecline}
    />
  );
}

interface CapabilityFlags {
  canApprove: boolean;
  canDecline: boolean;
  actionsRestricted: boolean;
}

// capabilityFlags mirrors erun-common's PlatformCapabilities.Known()/Allows()
// contract (see erun-ui's restrictedTenantDashboardRead, the same rule
// applied to the desktop's own invite-request actions): `known` is whether
// the capability *set itself* resolved to a real array, never whether the
// whoami query merely returned some response -- a 200 with `capabilities:
// null` (the platform reporting it could not resolve a set) is exactly as
// unknown as a query that is still loading or has errored outright. An
// unknown set must never render as "you may not do this": that hides a
// surface the caller can in fact use. It stays attemptable, and the
// server's own check on the real approve/decline call is what actually
// refuses it, same as every other capability-gated read/write in the repo.
function capabilityFlags(capabilities: PlatformCapability[] | undefined): CapabilityFlags {
  const known = capabilities !== undefined;
  if (!known) {
    return { canApprove: true, canDecline: true, actionsRestricted: false };
  }
  const canApprove = capabilityAllows(capabilities, 'POST', APPROVE_PATH_TEMPLATE);
  const canDecline = capabilityAllows(capabilities, 'POST', DECLINE_PATH_TEMPLATE);
  return { canApprove, canDecline, actionsRestricted: !canApprove && !canDecline };
}

// RequestDialogs holds the two dialogs RequestsPanel can show, split out
// purely to keep RequestsPanel's own branch count under the module's
// complexity budget.
function RequestDialogs({
  approved,
  pendingDecline,
  busy,
  onDismissApproved,
  onCancelDecline,
  onConfirmDecline,
}: {
  approved: InviteRequest | undefined;
  pendingDecline: InviteRequest | undefined;
  busy: boolean;
  onDismissApproved: () => void;
  onCancelDecline: () => void;
  onConfirmDecline: (reason: string) => void;
}): React.ReactElement {
  return (
    <>
      {approved?.mintedInviteToken !== undefined && (
        <IssuedInviteDialog request={approved} onDismiss={onDismissApproved} />
      )}
      {pendingDecline !== undefined && (
        <DeclineRequestDialog
          request={pendingDecline}
          busy={busy}
          onCancel={onCancelDecline}
          onConfirm={onConfirmDecline}
        />
      )}
    </>
  );
}

// RequestsPanel is the operator's invite-request queue: reachable by every
// tenant (JOIN_TENANT requests naming your own tenant),
// with Approve/Decline gated on whoami's capability set rather than tenant
// type -- see shell/sections.ts for the nav-level gate (every tenant sees
// this panel) and this component for the finer-grained action gate.
export function RequestsPanel({
  token,
  tenantType,
  rateLimitWindowSeconds,
}: {
  token: string;
  tenantType: string;
  rateLimitWindowSeconds: number;
}): React.ReactElement {
  const { requestsState, actionStates, approve, decline, approved, dismissApproved } =
    useRequestsController(token);
  const whoamiQuery = useGetWhoamiQuery(token);
  const { canApprove, canDecline, actionsRestricted } = capabilityFlags(
    whoamiQuery.data?.capabilities,
  );
  const [pendingDecline, setPendingDecline] = React.useState<InviteRequest | undefined>(undefined);

  return (
    <div className="grid gap-6">
      <Card aria-labelledby="requests-heading">
        <CardHeader>
          <CardTitle id="requests-heading">
            <Inbox className="mr-2 inline size-4" aria-hidden="true" />
            Requests
          </CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4">
          {whoamiQuery.isError && (
            <p className="text-sm text-destructive" role="alert">
              Could not check your permissions for this queue:{' '}
              {queryErrorMessage(whoamiQuery.error)}. Approve/Decline are still shown below; the
              platform will confirm on its own if either is genuinely restricted.
            </p>
          )}
          {actionsRestricted && (
            <p className="text-sm text-muted-foreground" role="status">
              You do not have permission to issue invitations or decline requests. You can still see
              the queue below.
            </p>
          )}
          <RequestsBody
            requestsState={requestsState}
            actionStates={actionStates}
            canApprove={canApprove}
            canDecline={canDecline}
            onApprove={approve}
            onRequestDecline={setPendingDecline}
          />
        </CardContent>
      </Card>
      {tenantType === 'OPERATIONS' && (
        <RateLimitPanel token={token} currentWindowSeconds={rateLimitWindowSeconds} />
      )}
      <RequestDialogs
        approved={approved}
        pendingDecline={pendingDecline}
        busy={
          pendingDecline !== undefined &&
          actionStates[pendingDecline.inviteRequestId]?.status === 'busy'
        }
        onDismissApproved={dismissApproved}
        onCancelDecline={() => {
          setPendingDecline(undefined);
        }}
        onConfirmDecline={(reason) => {
          if (pendingDecline === undefined) {
            return;
          }
          void decline(pendingDecline.inviteRequestId, reason).then(() => {
            setPendingDecline(undefined);
          });
        }}
      />
    </div>
  );
}
