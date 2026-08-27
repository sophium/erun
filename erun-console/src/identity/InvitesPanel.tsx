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
} from 'erun-kit';
import { Mail, UserPlus } from 'lucide-react';
import * as React from 'react';

import type { CreateInviteInput, Invite } from '../app/api/identityApi';
import type { CreateInviteState, InvitesState } from './controller';
import { useInvitesController } from './controller';

// acceptURL builds the invite link from the current origin — the backend
// has no reliable notion of its own public console URL, but the browser
// rendering this page always does.
function acceptURL(token: string): string {
  return `${window.location.origin}/accept-invite?token=${encodeURIComponent(token)}`;
}

// InviteLinkDialog shows the accept link once, right after creation — the
// same "shown once, copyable, dismiss clears it" shape UsersPanel's
// TemporaryPasswordDialog uses for a one-time credential.
function InviteLinkDialog({
  invite,
  onDismiss,
}: {
  invite: Invite;
  onDismiss: () => void;
}): React.ReactElement {
  const [copied, setCopied] = React.useState(false);
  const link = acceptURL(invite.token);

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
      <DialogContent aria-labelledby="invite-link-heading">
        <DialogHeader>
          <DialogTitle id="invite-link-heading">Invite created</DialogTitle>
          <DialogDescription>
            Share this link with {invite.email ?? 'the invitee'}. It expires{' '}
            {new Date(invite.expiresAt).toLocaleString()} and can only be used once.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <Input
            readOnly
            value={link}
            aria-label="Invite link"
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

function CreateInviteForm({
  createState,
  onCreate,
}: {
  createState: CreateInviteState;
  onCreate: (input: CreateInviteInput) => void;
}): React.ReactElement {
  const [email, setEmail] = React.useState('');
  const busy = createState.status === 'creating';

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    onCreate({ email: email.trim() || undefined });
    setEmail('');
  };

  return (
    <form className="grid max-w-md gap-3" onSubmit={submit} aria-labelledby="create-invite-heading">
      <h3 id="create-invite-heading" className="text-sm font-semibold text-foreground">
        Invite someone
      </h3>
      <div className="grid gap-2">
        <FieldLabel htmlFor="invite-email">Email (optional)</FieldLabel>
        <Input
          id="invite-email"
          type="email"
          value={email}
          onChange={(e) => {
            setEmail(e.target.value);
          }}
          placeholder="Leave blank to let the invitee choose"
        />
      </div>
      <Button type="submit" disabled={busy} className="justify-self-start">
        {busy ? 'Creating…' : 'Create invite'}
      </Button>
      {createState.status === 'error' && (
        <p className="text-sm text-destructive" role="alert">
          Could not create invite: {createState.message}
        </p>
      )}
    </form>
  );
}

function InviteRow({
  invite,
  onRevoke,
}: {
  invite: Invite;
  onRevoke: (inviteId: string) => void;
}): React.ReactElement {
  const [copied, setCopied] = React.useState(false);
  const copy = (): void => {
    void navigator.clipboard.writeText(acceptURL(invite.token)).then(() => {
      setCopied(true);
    });
  };
  return (
    <TableRow>
      <TableCell className="font-medium text-foreground">{invite.email ?? '(any email)'}</TableCell>
      <TableCell>{new Date(invite.expiresAt).toLocaleString()}</TableCell>
      <TableCell className="flex gap-2">
        <Button type="button" variant="outline" size="sm" onClick={copy}>
          {copied ? 'Copied' : 'Copy link'}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            onRevoke(invite.inviteId);
          }}
        >
          Revoke
        </Button>
      </TableCell>
    </TableRow>
  );
}

function InvitesTable({
  invites,
  onRevoke,
}: {
  invites: Invite[];
  onRevoke: (inviteId: string) => void;
}): React.ReactElement {
  if (invites.length === 0) {
    return <EmptyState icon={<Mail />} heading="No outstanding invites." />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Email</TableHead>
          <TableHead>Expires</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {invites.map((invite) => (
          <InviteRow key={invite.inviteId} invite={invite} onRevoke={onRevoke} />
        ))}
      </TableBody>
    </Table>
  );
}

function InvitesBody({
  invitesState,
  onRevoke,
}: {
  invitesState: InvitesState;
  onRevoke: (inviteId: string) => void;
}): React.ReactElement {
  if (invitesState.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading invites…
      </p>
    );
  }
  if (invitesState.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not load invites: {invitesState.message}
      </p>
    );
  }
  return <InvitesTable invites={invitesState.invites} onRevoke={onRevoke} />;
}

// InvitesPanel is the console's invite-only registration surface (#1483):
// create a revocable, single-use invite link for this tenant, list the
// outstanding ones, and revoke. Unlike Users/OrgSettings/SmtpSettings, this
// is not restricted to an OPERATIONS tenant — every tenant needs its own way
// to add members, since self-registration is closed (#1482).
export function InvitesPanel({ token }: { token: string }): React.ReactElement {
  const { invitesState, createState, create, revoke, dismissCreated } = useInvitesController(token);
  return (
    <Card aria-labelledby="invites-heading">
      <CardHeader>
        <CardTitle id="invites-heading">
          <UserPlus className="mr-2 inline size-4" aria-hidden="true" />
          Invites
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-6">
        <InvitesBody invitesState={invitesState} onRevoke={(id) => void revoke(id)} />
        <CreateInviteForm createState={createState} onCreate={create} />
      </CardContent>
      {createState.status === 'created' && (
        <InviteLinkDialog invite={createState.invite} onDismiss={dismissCreated} />
      )}
    </Card>
  );
}
