import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { versionNoticeMessage } from '@/app/versionSuggestions';
import type { UIVersionSuggestionNotice } from '@/types';

// VersionNotices explains why a runtime-image source contributed no versions.
export function VersionNotices({
  notices,
}: {
  notices: UIVersionSuggestionNotice[];
}): React.ReactElement | null {
  if (notices.length === 0) {
    return null;
  }
  return (
    <ul
      className="flex flex-col gap-1.5 rounded-[var(--radius)] border border-border bg-muted/40 px-3 py-2"
      aria-label="Version source notices"
    >
      {notices.map((notice) => (
        <li
          key={`${notice.image}:${notice.kind}`}
          className="flex items-start gap-2 text-xs leading-snug text-muted-foreground"
        >
          <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-amber-600" />
          <span className="[overflow-wrap:anywhere]">{versionNoticeMessage(notice)}</span>
        </li>
      ))}
    </ul>
  );
}
