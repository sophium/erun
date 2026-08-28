// erun-kit deliberately departs from stock shadcn output in exactly one place.
//
// Stock DialogContent is `display: grid` with no explicit grid-template-columns,
// so the browser sizes the implicit single column to the max-content width of
// its widest descendant. One unbroken string -- a long file path, a UUID -- with
// no `min-w-0` above it drags that column, and every sibling grid item with it,
// past the card's own clamped box. The card's background and border still paint
// at the correct width, so the break shows only as in-flow content spilling over
// whatever is behind the dialog.
//
// `shadcn:check` regenerates the primitives and requires the tree to match, so
// the departure has to be reapplied after regeneration rather than merely
// documented. Each replacement asserts its stock text is present first: if
// upstream shadcn changes dialog.tsx, this fails loudly instead of silently
// masking the change, which is exactly what the check exists to catch.
import { readFileSync, writeFileSync } from 'node:fs';

const FILE = 'src/components/ui/dialog.tsx';
const REPLACEMENTS = [
  {
    what: 'clamp the content grid track so it cannot outgrow the card',
    from: 'z-50 grid w-full max-w-[calc(100%-2rem)]',
    to: 'z-50 grid grid-cols-1 w-full max-w-[calc(100%-2rem)]',
  },
  {
    what: 'let the header shrink inside the clamped track',
    from: 'cn("flex flex-col gap-2 text-center sm:text-left", className)',
    to: 'cn("flex min-w-0 flex-col gap-2 text-center sm:text-left", className)',
  },
  {
    what: 'let the footer shrink inside the clamped track',
    from: '"flex flex-col-reverse gap-2 sm:flex-row sm:justify-end"',
    to: '"flex min-w-0 flex-col-reverse gap-2 sm:flex-row sm:justify-end"',
  },
];

let source = readFileSync(FILE, 'utf8');
for (const { what, from, to } of REPLACEMENTS) {
  if (source.includes(to)) continue;
  if (!source.includes(from)) {
    console.error(
      `reapply-dialog-clamp: could not find the stock text for "${what}" in ${FILE}.\n` +
        `Expected to find:\n  ${from}\n` +
        `Upstream shadcn has changed this primitive. Re-derive the clamp against the new output ` +
        `and update this script -- do not delete it, or the dialog overflow regression returns.`,
    );
    process.exit(1);
  }
  source = source.replace(from, to);
}
writeFileSync(FILE, source);
