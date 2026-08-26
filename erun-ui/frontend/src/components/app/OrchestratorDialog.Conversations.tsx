import { Button, EmptyState, Label, StatusBadge } from 'erun-kit';
import { LoaderCircle, MessagesSquare } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import {
  attachOrchestratorConversation,
  detachOrchestratorConversation,
} from '@/app/orchestratorThunks';
import {
  formatTranscriptSize,
  omittedSummary,
  resumingSummary,
  roleLabel,
} from '@/components/app/OrchestratorDialog.Conversations.helpers';

import { ListOrchestratorConversations } from '../../../wailsjs/go/main/App';
import { relativeTimeFromNow } from './ManageDialog.helpers';

type Conversation = Awaited<
  ReturnType<typeof ListOrchestratorConversations>
>['conversations'][number];
type Listing = Awaited<ReturnType<typeof ListOrchestratorConversations>>;

// useOrchestratorConversations loads what this orchestrator could resume. Read
// only: opening the dialog inspects the transcripts on this machine and starts
// nothing.
function useOrchestratorConversations(orchestratorId: string): {
  listing: Listing | null;
  failure: string;
} {
  const [listing, setListing] = React.useState<Listing | null>(null);
  const [failure, setFailure] = React.useState('');

  React.useEffect(() => {
    let active = true;
    setListing(null);
    setFailure('');
    void ListOrchestratorConversations(orchestratorId).then(
      (resolved) => {
        if (active) {
          setListing(resolved);
        }
      },
      (error: unknown) => {
        if (active) {
          setFailure(error instanceof Error ? error.message : String(error));
        }
      },
    );
    return () => {
      active = false;
    };
  }, [orchestratorId]);

  return { listing, failure };
}

// OrchestratorConversationsSection is how a wrong resume is corrected without
// leaving the app. An orchestrator normally resumes the conversation its own
// session was last live on, and when that cannot be confirmed it falls back to
// the one its name resolves to — which is the case where the work sits in a
// conversation nothing is attached to. This lists every conversation the
// orchestrator could be pointed at, says which one it is on and why, and
// attaches another.
//
// Conversations belonging to other orchestrators are not offered: handing one
// orchestrator another's history is the failure this whole area exists to
// prevent. What was left out is stated rather than hidden.
export function OrchestratorConversationsSection({
  orchestratorId,
}: {
  orchestratorId: string;
}): React.ReactElement {
  const { listing, failure } = useOrchestratorConversations(orchestratorId);
  const omitted = listing
    ? omittedSummary(listing.omittedNotMine ?? 0, listing.omittedForCap ?? 0)
    : '';
  return (
    <div className="space-y-1.5">
      <Label>Conversation</Label>
      {listing ? (
        <p className="text-xs text-muted-foreground">
          {resumingSummary(listing.resumingSource)}
          {listing.notice ? ` ${listing.notice}` : ''}
        </p>
      ) : null}
      {failure ? (
        <p role="alert" className="text-sm break-words text-destructive">
          {failure}
        </p>
      ) : null}
      <ConversationList orchestratorId={orchestratorId} listing={listing} failed={failure !== ''} />
      {omitted ? <p className="text-xs text-muted-foreground">{omitted}</p> : null}
    </div>
  );
}

// ConversationList renders the three states the listing itself can be in —
// still being read, nothing here yet, and the rows — so each has a surface of
// its own rather than one collapsing into another. A machine with no
// conversations gets a purpose-built empty state, never an empty box that reads
// as a control.
function ConversationList({
  orchestratorId,
  listing,
  failed,
}: {
  orchestratorId: string;
  listing: Listing | null;
  failed: boolean;
}): React.ReactElement | null {
  if (!listing) {
    return failed ? null : (
      <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
        <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
        Reading this machine’s conversations…
      </p>
    );
  }
  if (listing.conversations.length === 0) {
    return (
      <EmptyState
        icon={<MessagesSquare />}
        heading="No conversations on this machine yet"
        body="This orchestrator starts a new one the first time you run it."
      />
    );
  }
  return (
    <div className="max-h-64 space-y-2 overflow-y-auto">
      {listing.conversations.map((conversation) => (
        <ConversationRow
          key={conversation.conversationId}
          orchestratorId={orchestratorId}
          conversation={conversation}
          attached={listing.attached ?? ''}
        />
      ))}
    </div>
  );
}

function ConversationRow({
  orchestratorId,
  conversation,
  attached,
}: {
  orchestratorId: string;
  conversation: Conversation;
  attached: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const role = roleLabel(conversation.role);
  const isAttached = attached !== '' && attached === conversation.conversationId;
  const written =
    conversation.lastWrittenUnix > 0
      ? relativeTimeFromNow(conversation.lastWrittenUnix * 1000)
      : 'never written';
  return (
    <div
      className="rounded-sm border border-border/60 px-2 py-1.5"
      data-conversation-id={conversation.conversationId}
      data-conversation-role={conversation.role}
      data-conversation-resuming={conversation.resuming ? 'true' : 'false'}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 space-y-0.5">
          <div className="flex items-center gap-1.5">
            <StatusBadge tone={role.tone} label={role.label} />
            {conversation.resuming ? (
              <StatusBadge tone="in-progress" label="Resumes now" showIcon={false} />
            ) : null}
          </div>
          <p className="truncate font-mono text-xs">{conversation.conversationId}</p>
          <p className="text-xs text-muted-foreground">
            {written} · {formatTranscriptSize(conversation.sizeBytes)}
            {conversation.folder ? ` · ${conversation.folder}` : ''}
          </p>
          {conversation.excerpt ? (
            <p className="truncate text-xs text-muted-foreground italic">{conversation.excerpt}</p>
          ) : null}
          <p className="text-xs text-muted-foreground">{role.note}</p>
        </div>
        <div className="flex flex-none">
          {isAttached ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-label={`Stop using ${conversation.conversationId} for this orchestrator`}
              onClick={() => {
                void dispatch(detachOrchestratorConversation(orchestratorId));
              }}
            >
              Use the default
            </Button>
          ) : (
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-label={`Attach ${conversation.conversationId} to this orchestrator`}
              onClick={() => {
                void dispatch(
                  attachOrchestratorConversation(orchestratorId, conversation.conversationId),
                );
              }}
            >
              Attach
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
