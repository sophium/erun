import '@testing-library/jest-dom/vitest';

import { configure } from '@testing-library/dom';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

// findBy*/waitFor use their own timeout (default 1000ms), which flakes under
// gate CPU contention; it stays well under vite.config.ts's testTimeout.
configure({ asyncUtilTimeout: 15000 });

// jsdom lacks ResizeObserver (probed by Radix) and matchMedia.
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
// Radix Select calls scrollIntoView, which jsdom lacks.
if (typeof Element.prototype.scrollIntoView === 'undefined') {
  Element.prototype.scrollIntoView = (): undefined => undefined;
}

// This runner exposes no window.localStorage, so provide an in-memory one.
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

// Radix's FocusScope dispatches its unmount event from a 0ms timer. Unmount, then let
// that timer run while this file's jsdom is alive: fired after teardown, it throws.
export async function unmountAndFlushTimers(): Promise<void> {
  cleanup();
  await new Promise((resolve) => setTimeout(resolve, 0));
}

afterEach(unmountAndFlushTimers);
