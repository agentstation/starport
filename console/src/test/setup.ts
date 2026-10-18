import { configure } from "@testing-library/react";

// A route test waits for its listing with findBy and waitFor. The Testing
// Library default gives those one second, and a loaded CI runner renders a
// query-backed listing more slowly than that, so the wait gets four seconds
// and vitest.config.ts raises the test ceiling to match.
configure({ asyncUtilTimeout: 4000 });

// jsdom lacks ResizeObserver and scrollIntoView. cmdk measures its list
// with the first and every keyboard list scrolls with the second, so
// both get a no-op stand-in before any test file loads.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

if (!("ResizeObserver" in globalThis)) {
  Object.assign(globalThis, { ResizeObserver: ResizeObserverStub });
}

if (typeof Element !== "undefined" && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}
