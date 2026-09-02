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
