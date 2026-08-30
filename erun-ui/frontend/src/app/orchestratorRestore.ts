import type { OrchestratorInfo } from './slices/orchestratorsSlice';

// One orchestrator in alsoReopen: its id and the conversation it resumes. The
// backend resolves conversationId the same way for every entry, owner
// included — the exact conversation last known to be running for that
// orchestrator, never a re-derivation from its id, which can land on a
// different, older conversation. Empty only when the backend found
// nothing safe to resume, in which case the orchestrator starts fresh.
export interface OrchestratorReopenRef {
  orchestratorId: string;
  conversationId?: string;
}

// OrchestratorNoticeKind mirrors the Go side's orchestratorNoticeKind: 'info'
// is reserved for a routine notice (none is minted today — a launch resumes
// the derived anchor with nothing to report unless an explicit attachment
// failed, erun#1696), 'warning' is every resolution this restore actually
// reports. 'unknown' is a frontend-only
// value for a notice whose kind this launch could not classify — a raw
// payload missing the field, or naming something other than the two kinds the
// backend ever mints. It exists so that case renders as its own visibly
// distinct thing rather than defaulting to 'info' (which would hide a real
// problem behind a routine-looking notice) or to 'warning' (which would cry
// wolf over what might be entirely routine).
export type OrchestratorNoticeKind = 'info' | 'warning' | 'unknown';

// OrchestratorNotice is one operator-facing notice about a reopened
// orchestrator: which one it is about (absent when it names several, such as
// the hand-offs-not-reopened summary), how loudly it reads, and the text
// itself.
export interface OrchestratorNotice {
  orchestratorId?: string;
  kind: OrchestratorNoticeKind;
  text: string;
}

// RawOrchestratorNotice is the wire shape one entry in the backend's notices
// list can actually carry: kind and text are typed as optional/untrusted here
// even though the Go side always sets them, because the payload crosses a
// process boundary and a stale or hand-crafted one must degrade rather than
// throw.
interface RawOrchestratorNotice {
  orchestratorId?: string;
  kind?: string;
  text?: string;
}

// What the backend answers when boot asks which orchestrators to reopen:
// orchestratorId is the one that OWNS THE TERMINAL PANE — the pane is single,
// so exactly one orchestrator gets it — and alsoReopen lists every other
// orchestrator that was open too, restored alongside it but idle. Only the
// pane owner can carry a resume PROMPT; that is why resumePrompt lives on the
// target itself rather than per id in alsoReopen. notices carries one entry
// per surprising resolution — the pane owner's, or another reopened
// orchestrator's — each with its own kind, rather than one joined string that
// would flatten several different severities together.
export interface OrchestratorReopenTarget {
  orchestratorId?: string;
  conversationId?: string;
  resumePrompt?: string;
  alsoReopen?: OrchestratorReopenRef[];
  notices?: RawOrchestratorNotice[];
}

// How boot should reopen one orchestrator: which one, which of its
// conversations, and whether that conversation is handed a task on resume.
export interface OrchestratorRestorePlan {
  id: string;
  conversationId: string;
  resumePrompt: string;
}

// OrchestratorRestoreOutcome is what a launch actually restores: the
// orchestrator that ends up owning the pane (or null if there is nothing valid
// to reopen), and every other orchestrator that comes back idle beside it,
// each with the conversation it resumes.
export interface OrchestratorRestoreOutcome {
  primary: OrchestratorRestorePlan | null;
  alsoReopen: OrchestratorReopenRef[];
}

const trimmed = (value: string | undefined): string => value?.trim() ?? '';

// normalizeOrchestratorNoticeKind maps the raw wire value to one of the two
// kinds the backend actually mints, or 'unknown' for anything else -- absent,
// misspelled, or from a payload written by a backend version that classified
// notices differently. See OrchestratorNoticeKind for why 'unknown' is its own
// case rather than folded into either of the other two.
function normalizeOrchestratorNoticeKind(kind: string | undefined): OrchestratorNoticeKind {
  if (kind === 'info' || kind === 'warning') {
    return kind;
  }
  return 'unknown';
}

// readRestoreNotices is every notice the backend attached to the target, each
// with its own kind. It reads tolerantly because the target crosses a process
// boundary: a payload that omits the field, or one entry within it, must
// degrade rather than throw and take the whole restore down with it.
export function readRestoreNotices(
  target: OrchestratorReopenTarget | null | undefined,
): OrchestratorNotice[] {
  return (target?.notices ?? []).reduce<OrchestratorNotice[]>((kept, raw) => {
    const text = trimmed(raw.text);
    if (text === '') {
      return kept;
    }
    kept.push({
      orchestratorId: trimmed(raw.orchestratorId) || undefined,
      kind: normalizeOrchestratorNoticeKind(raw.kind),
      text,
    });
    return kept;
  }, []);
}

// planOrchestratorRestore decides what a launch actually restores, given the
// target and the definitions that exist right now. A candidate naming an
// orchestrator that has since been deleted restores nothing for that
// candidate, and neither does a transient (Investigate) session, which has no
// definition to resume — both are dropped rather than starting a session for
// an id that cannot come back.
//
// If the backend's chosen pane owner cannot be honored this way (deleted
// between shutdown and this launch), the next most-recently-opened surviving
// orchestrator takes the pane instead — the same recency rule the backend used
// to pick a pane owner, just re-applied to the ones still around — rather than
// silently dropping to the default environment selection while other
// orchestrators still start up in the background. Only when nothing in the
// whole set survives does boot fall through to that default.
export function planOrchestratorRestore(
  target: OrchestratorReopenTarget | null | undefined,
  orchestrators: readonly OrchestratorInfo[],
): OrchestratorRestoreOutcome {
  const source: OrchestratorReopenTarget = target ?? {};
  const exists = (id: string): boolean =>
    orchestrators.some((orchestrator) => orchestrator.id === id && !orchestrator.transient);

  const primaryID = trimmed(source.orchestratorId);
  const seen = new Set<string>();
  const survivingAlso = (source.alsoReopen ?? []).reduce<OrchestratorReopenRef[]>((kept, raw) => {
    const id = trimmed(raw.orchestratorId);
    if (id === '' || id === primaryID || seen.has(id) || !exists(id)) {
      return kept;
    }
    seen.add(id);
    kept.push({ orchestratorId: id, conversationId: trimmed(raw.conversationId) });
    return kept;
  }, []);

  if (primaryID && exists(primaryID)) {
    return {
      primary: {
        id: primaryID,
        conversationId: trimmed(source.conversationId),
        resumePrompt: trimmed(source.resumePrompt),
      },
      alsoReopen: survivingAlso,
    };
  }
  const rest = [...survivingAlso];
  const promoted = rest.pop();
  if (promoted === undefined) {
    return { primary: null, alsoReopen: [] };
  }
  return {
    // The promoted survivor carries the conversation the backend already
    // resolved for it as an alsoReopen entry — it must not be dropped just
    // because this orchestrator is now taking the pane instead.
    primary: {
      id: promoted.orchestratorId,
      conversationId: promoted.conversationId ?? '',
      resumePrompt: '',
    },
    alsoReopen: rest,
  };
}
