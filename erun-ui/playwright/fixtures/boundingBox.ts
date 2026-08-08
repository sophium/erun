import { expect, type Locator } from '@playwright/test';

// Geometry helper for the layout specs. boundingBox() is nullable, and
// narrowing it inside a test body is a conditional — which the flake rules ban,
// because a spec that returns early on a null box silently asserts nothing. The
// check lives here instead, so a missing box fails the spec by name.

export interface ElementBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

export async function boundingBoxOf(locator: Locator, label: string): Promise<ElementBox> {
  const box = await locator.boundingBox();
  expect(box, `${label} has no bounding box`).not.toBeNull();
  if (box === null) {
    throw new Error(`${label} has no bounding box`);
  }
  return box;
}
