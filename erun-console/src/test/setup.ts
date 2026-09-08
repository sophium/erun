import '@testing-library/jest-dom/vitest';

import { configure } from '@testing-library/dom';

// vite.config.ts's testTimeout only bounds the whole test; findBy*/waitFor use
// this separate, testing-library-owned timeout instead, which defaults to
// 1000ms. Left at the default, a component test that renders slowly under the
// same gate contention testTimeout was raised for still fails its query well
// before the 60s bound has any chance to matter. Reproduced locally the same
// way: saturating every core with busy loops made a query at the 1000ms
// default fail intermittently while the whole suite (266 tests) still passed
// cleanly, every time, once this was raised to 15000ms. 15000ms stays well
// under testTimeout, so a genuine hang still fails as a test timeout with a
// useful message rather than as a bare "unable to find an element".
configure({ asyncUtilTimeout: 15000 });

// jsdom implements neither API. Radix primitives (Select, Checkbox) probe
// ResizeObserver on mount regardless of whether a test ever resizes anything,
// and matchMedia backs the console's dark-mode preference detection.
class ResizeObserverStub {
  observe(): undefined {
    return undefined;
  }
  unobserve(): undefined {
    return undefined;
  }
  disconnect(): undefined {
    return undefined;
  }
}

function matchMediaStub(query: string): MediaQueryList {
  return {
    matches: false,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  };
}

if (typeof window.ResizeObserver === 'undefined') {
  window.ResizeObserver = ResizeObserverStub;
}
if (typeof window.matchMedia === 'undefined') {
  window.matchMedia = matchMediaStub;
}
// jsdom has no layout engine, so Element.prototype.scrollIntoView does not
// exist; Radix's Select scrolls its highlighted item into view whenever the
// listbox opens, which throws with no stub here.
if (typeof Element.prototype.scrollIntoView === 'undefined') {
  Element.prototype.scrollIntoView = (): undefined => undefined;
}
