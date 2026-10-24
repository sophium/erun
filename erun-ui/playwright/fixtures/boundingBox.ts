import { expect, type Locator } from '@playwright/test';

// A null box fails the spec by name here, since tests may not branch on it.

export interface ElementBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

// boundingBox() otherwise waits out the whole test; `timeoutMs` bounds one attempt so callers can retry.
export async function boundingBoxOf(
  locator: Locator,
  label: string,
  timeoutMs?: number,
): Promise<ElementBox> {
  const box = await locator.boundingBox(
    timeoutMs === undefined ? undefined : { timeout: timeoutMs },
  );
  expect(box, `${label} has no bounding box`).not.toBeNull();
  if (box === null) {
    throw new Error(`${label} has no bounding box`);
  }
  return box;
}

// Without explicit grid columns an unbreakable descendant widens the card's grid track while the
// card itself paints clamped, so overflow shows only by measuring descendants.
export interface OverflowingDescendant {
  tag: string;
  dataSlot: string | null;
  overflowRight: number;
  rectWidth: number;
  cardWidth: number;
}

async function overflowingDescendants(card: Locator): Promise<OverflowingDescendant[]> {
  return card.evaluate((cardEl) => {
    const cardRect = cardEl.getBoundingClientRect();
    const entries: OverflowingDescendant[] = [];
    // Descendants inside a scroll container are clipped and cannot overflow visibly.
    const isClipped = (el: Element): boolean => {
      for (let a = el.parentElement; a && a !== cardEl; a = a.parentElement) {
        const style = getComputedStyle(a);
        if (style.overflowX !== 'visible' || style.overflowY !== 'visible') return true;
      }
      return false;
    };
    const walker = document.createTreeWalker(cardEl, NodeFilter.SHOW_ELEMENT);
    let node: Node | null = walker.currentNode;
    while (node) {
      const el = node as Element;
      const rect = el.getBoundingClientRect();
      // 1px tolerance absorbs sub-pixel rounding.
      if (rect.width > 0 && rect.height > 0 && rect.right - cardRect.right > 1 && !isClipped(el)) {
        entries.push({
          tag: el.tagName,
          dataSlot: el.getAttribute('data-slot'),
          overflowRight: rect.right - cardRect.right,
          rectWidth: rect.width,
          cardWidth: cardRect.width,
        });
      }
      node = walker.nextNode();
    }
    return entries;
  });
}

export async function expectDialogContentStaysWithinCard(
  card: Locator,
  label: string,
): Promise<void> {
  const entries = await overflowingDescendants(card);
  // One blown-out track drags every descendant along; the widest few are the culprits.
  const worst = [...entries]
    .sort((a, b) => b.rectWidth - a.rectWidth)
    .slice(0, 5)
    .map(
      (e) =>
        `${e.tag}${e.dataSlot ? `[${e.dataSlot}]` : ''} ${e.rectWidth.toFixed(0)}px ` +
        `in a ${e.cardWidth.toFixed(0)}px card (${e.overflowRight.toFixed(0)}px past its right edge)`,
    );
  expect(
    entries,
    `${label}: ${entries.length} descendant(s) render wider than the card. Widest:\n  ` +
      worst.join('\n  '),
  ).toEqual([]);
}
