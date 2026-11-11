import { beforeEach, describe, expect, it, vi } from "vitest";

// The admin's entry sets up the shared modules its pages use, in order (see
// app/entries/entries.test.ts, the reader's).
const modules: Record<string, string[]> = {
  "../../shared/lib/busy-forms": ["init"],
  "../features/select": ["init"],
};

const entries: Record<string, string[]> = {
  admin: ["busy-forms", "select"],
};

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
