import type { StatusBadgeTone } from 'erun-kit';

export interface ConversationRoleLabel {
  tone: StatusBadgeTone;
  label: string;
  note: string;
}

// roleLabel renders a row's role in the operator's language, with the tone that
// matches what it means for them. "Stranded" is the warning: something recorded
// that conversation as live and nothing can vouch for it, so it is the row most
// likely to hold work nobody is resuming.
export function roleLabel(role: string): ConversationRoleLabel {
  switch (role) {
    case 'attached':
      return { tone: 'success', label: 'Attached', note: 'You chose this conversation.' };
    case 'live':
      return {
        tone: 'success',
        label: 'Live',
        note: 'This orchestrator’s own session reported being on it.',
      };
    case 'stranded':
      return {
        tone: 'warning',
        label: 'Stranded',
        note: 'Recorded as live by a session erun can no longer confirm.',
      };
    case 'derived':
      return {
        tone: 'muted',
        label: 'Default',
        note: 'The conversation this orchestrator’s name resolves to.',
      };
    default:
      return { tone: 'muted', label: 'Unclaimed', note: 'No orchestrator is using it.' };
  }
}

// resumingSummary says, in one line, which conversation a launch resumes now and
// why — the question the whole surface exists to answer. Only two sources ever
// arrive here: an explicit attachment, or the derived anchor a launch resumes
// by default (erun#1696) — a tracked conversation is never adopted on its own.
export function resumingSummary(source: string): string {
  switch (source) {
    case 'attached':
      return 'Resuming the conversation you attached.';
    default:
      return 'Resuming the conversation this orchestrator’s name resolves to.';
  }
}

// formatTranscriptSize renders a transcript's size at the precision an operator
// compares two of them at: a conversation holding hours of work is megabytes,
// one that stopped at its first turn is kilobytes.
export function formatTranscriptSize(bytes: number): string {
  if (bytes <= 0) {
    return 'not started';
  }
  if (bytes < 1024) {
    return `${String(bytes)} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${String(Math.round(bytes / 1024))} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

// omittedSummary names what the listing did not show, so a short list is never
// mistaken for the whole machine.
export function omittedSummary(omittedNotMine: number, omittedForCap: number): string {
  const parts: string[] = [];
  if (omittedNotMine > 0) {
    parts.push(
      `${String(omittedNotMine)} ${omittedNotMine === 1 ? 'conversation belongs' : 'conversations belong'} to other orchestrators and ${omittedNotMine === 1 ? 'is' : 'are'} not offered here`,
    );
  }
  if (omittedForCap > 0) {
    parts.push(`${String(omittedForCap)} older ${omittedForCap === 1 ? 'one' : 'ones'} not shown`);
  }
  return parts.length === 0 ? '' : `${parts.join('; ')}.`;
}
