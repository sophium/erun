import { Button, Popover, PopoverContent, PopoverTrigger } from 'erun-kit';
import { Keyboard } from 'lucide-react';
import * as React from 'react';

import { REVIEW_KEYBOARD_SHORTCUTS } from '@/app/reviewKeyboardShortcuts';

// ReviewKeyboardShortcutsHint is the review surface's discoverability
// affordance for its keyboard model (erun-ui/AGENTS.md § "The keyboard model
// the review surface still owes"): the repo has no existing shortcuts-help
// precedent to follow, and native `title` is not acceptable for meaningful UI
// (erun-ui/AGENTS.md § "Frontend Component Discovery"), so this is the
// smallest thing that makes the bindings discoverable -- a labelled icon
// button that opens a real Popover listing every binding, rendered from both
// the diff panel (DiffList.tsx) and the review detail dialog
// (ReviewDetailDialog.tsx) so either entry point teaches the whole model.
export function ReviewKeyboardShortcutsHint(): React.ReactElement {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Keyboard shortcuts"
          className="size-7 cursor-pointer border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-[15px]"
        >
          <Keyboard aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-72">
        <div className="grid gap-1.5">
          <div className="text-sm font-semibold text-foreground">Keyboard shortcuts</div>
          {REVIEW_KEYBOARD_SHORTCUTS.map((shortcut) => (
            <div
              key={`${shortcut.keys}:${shortcut.action}`}
              className="flex items-center justify-between gap-3 text-[13px]"
            >
              <span className="text-muted-foreground">{shortcut.action}</span>
              <kbd className="rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-foreground">
                {shortcut.keys}
              </kbd>
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
