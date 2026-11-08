// Helpers for the tests of modules that listen on document or window, which
// stay the same for every test in a file.
import { afterEach, beforeEach, vi } from "vitest";

type Listener = [
  EventTarget,
  string,
  EventListenerOrEventListenerObject | null,
  boolean | EventListenerOptions | undefined,
];

// isolate gives each test a fresh copy of the modules it imports (their
// state, like the live region or a memoized value, starts over), an empty
// body, and removes the listeners the test added to anything. Call it at the
// top of a test file and import the module under test in each test (or in a
// beforeEach) with await import(...).
export function isolate(): void {
  let added: Listener[] = [];
  // happy-dom's EventTarget, which document and elements inherit from (the
  // global EventTarget can be Node's own).
  let proto: EventTarget = document;
  while (!Object.prototype.hasOwnProperty.call(proto, "addEventListener")) proto = Object.getPrototypeOf(proto);
  const add = proto.addEventListener;
  const addToWindow = window.addEventListener;
  const capture = (options?: boolean | AddEventListenerOptions) =>
    typeof options === "object" ? { capture: options.capture } : options;
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = "";
    added = [];
    vi.spyOn(proto, "addEventListener").mockImplementation(function (this: EventTarget, type, listener, options) {
      added.push([this, type, listener, capture(options)]);
      add.call(this, type, listener, options);
    });
    // In Vitest's happy-dom environment, window is the global object, whose
    // addEventListener is its own (bound) function, not EventTarget's.
    vi.spyOn(window, "addEventListener").mockImplementation((type, listener, options) => {
      added.push([window, type, listener, capture(options)]);
      addToWindow(type, listener, options);
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    for (const [target, type, listener, options] of added) target.removeEventListener(type, listener, options);
  });
}

// pageTransition dispatches a pageshow or pagehide event on window, persisted
// or not (happy-dom has no PageTransitionEvent).
export function pageTransition(type: "pageshow" | "pagehide", persisted: boolean): void {
  const e = new Event(type);
  Object.defineProperty(e, "persisted", { value: persisted });
  window.dispatchEvent(e);
}

// submit dispatches a submit event for form, from submitter if given, and
// returns whether a listener prevented it (the form's own submission, which
// happy-dom would try to perform, never runs).
export function submit(form: HTMLFormElement, submitter?: HTMLElement): boolean {
  const e = new SubmitEvent("submit", { bubbles: true, cancelable: true, submitter });
  form.dispatchEvent(e);
  return e.defaultPrevented;
}

// liveRegion returns what the polite live region says, "" when there is none.
export function liveRegion(): string {
  return document.querySelector("p[aria-live=polite]")?.textContent ?? "";
}
