// erun-kit departs from stock shadcn in one place: DialogContent's implicit grid
// column grows to its widest unbroken string, spilling content past the card.
// shadcn:check regenerates primitives, so this reapplies the clamp and fails if
// the stock text it replaces has changed.
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
