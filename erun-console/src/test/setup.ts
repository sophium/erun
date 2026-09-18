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

// The jsdom library implements both Storage APIs, but this environment does not
// expose them: a bare `new JSDOM('', {url})` has window.localStorage, while under
// this runner window.localStorage is undefined and every component that reads a
// persisted preference throws on mount. In-memory is enough for a test -- nothing
// here reloads a page, so persistence across navigations is not observable in a
// component test, and nothing is shared between test files.
class MemoryStorage implements Storage {
  private readonly entries = new Map<string, string>();

  get length(): number {
    return this.entries.size;
  }

  clear(): void {
    this.entries.clear();
  }

  getItem(key: string): string | null {
    return this.entries.get(key) ?? null;
  }

  key(index: number): string | null {
    return [...this.entries.keys()][index] ?? null;
  }

  removeItem(key: string): void {
    this.entries.delete(key);
  }

  setItem(key: string, value: string): void {
    this.entries.set(key, value);
  }
}

if (typeof window.localStorage === 'undefined') {
  Object.defineProperty(window, 'localStorage', { value: new MemoryStorage(), configurable: true });
}
if (typeof window.sessionStorage === 'undefined') {
  Object.defineProperty(window, 'sessionStorage', {
    value: new MemoryStorage(),
    configurable: true,
  });
}
