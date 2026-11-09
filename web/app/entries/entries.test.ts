import { beforeEach, describe, expect, it, vi } from "vitest";

// Each page group's entry sets up the modules its pages use, in an order
// that matters where two listen for the same event: save-star before
// busy-forms, back-forward before timeline-tabs. Every entry sets up rum
// first. A dropped import or a reordered setup fails here.

// Every module an entry can set up, by its path from here, and the setup
// functions it exports.
const modules: Record<string, string[]> = {
  "../../shared/lib/busy-forms": ["init"],
  "../../shared/lib/local-times": ["localizeTimes"],
  "../../shared/lib/password-toggle": ["init"],
  "../features/back-forward": ["init"],
  "../features/import-status": ["init"],
  "../features/infinite-scroll": ["init"],
  "../features/live-search": ["init"],
  "../features/lookup-status": ["init"],
  "../features/read-pings": ["enhancePings"],
  "../features/reorder": ["init"],
  "../features/rum": ["setupRUM"],
  "../features/save-star": ["init"],
  "../features/seen-beacon": ["init"],
  "../features/share": ["init"],
  "../features/timeline-form": ["init"],
  "../features/timeline-tabs": ["init"],
  "../features/video-embeds": ["init"],
};

// The setups each entry runs, in order, named by module.
const entries: Record<string, string[]> = {
  reading: [
    "rum",
    "local-times",
    "read-pings",
    "back-forward",
    "save-star",
    "share",
    "busy-forms",
    "video-embeds",
    "timeline-tabs",
    "seen-beacon",
    "infinite-scroll",
  ],
  search: ["rum", "back-forward", "busy-forms", "live-search", "lookup-status"],
  timelines: ["rum", "back-forward", "timeline-form", "reorder"],
  account: ["rum", "local-times", "back-forward", "busy-forms", "import-status", "password-toggle"],
  login: ["rum", "back-forward", "busy-forms", "password-toggle"],
  settings: ["rum"],
  solo: ["rum"],
};

// The entries, loaded on demand, so each test can import one afresh.
const load = import.meta.glob(["./*.ts", "!./*.test.ts"]);

let calls: string[] = [];

beforeEach(() => {
  vi.resetModules();
  calls = [];
  for (const [path, setups] of Object.entries(modules)) {
    const name = path.slice(path.lastIndexOf("/") + 1);
    vi.doMock(path, () => Object.fromEntries(setups.map((fn) => [fn, () => calls.push(name)])));
  }
});

describe("entries", () => {
  for (const [entry, want] of Object.entries(entries)) {
    it(`${entry} sets up its modules in order`, async () => {
      await load[`./${entry}.ts`]();
      expect(calls).toEqual(want);
    });
  }

  it("covers every entry", () => {
    expect(Object.keys(load).sort()).toEqual(
      Object.keys(entries)
        .map((e) => `./${e}.ts`)
        .sort(),
    );
  });
});
