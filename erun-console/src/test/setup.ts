import '@testing-library/jest-dom/vitest';

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
