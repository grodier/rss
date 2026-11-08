import { describe, expect, it } from "vitest";
import cases from "./testdata/timeline-summary.json";
import { joinAnd, joinOr, rate, summary, terms, type Feed, type Fields, type Summary } from "./timeline-form";

// The summary is built in Go and rebuilt here: Go is the spec. The fixture is
// written by TestTimelineSummaryFixture (internal/server/timelines_test.go).
describe("summary", () => {
  const fixture = cases as { name: string; feeds: Feed[]; fields: Fields; summary: Summary }[];

  it("has the fixture's cases", () => {
    expect(fixture.length).toBeGreaterThan(20);
  });

  it.each(fixture.map((c) => [c.name, c] as const))("%s", (_, c) => {
    expect(summary(c.feeds, c.fields)).toEqual(c.summary);
  });
});

describe("the words", () => {
  it("joins with commas and a last word", () => {
    expect(joinOr([])).toBe("");
    expect(joinOr(["A"])).toBe("A");
    expect(joinOr(["A", "B"])).toBe("A or B");
    expect(joinAnd(["A", "B", "C"])).toBe("A, B and C");
  });

  it("splits terms like splitTerms", () => {
    expect(terms(" AI ,  big   data, ai,,")).toEqual(["AI", "big data"]);
    expect(terms("")).toEqual([]);
  });

  it("reads the rate like maxPostsPerWeek", () => {
    expect(rate("3")).toBe(3);
    expect(rate(" 100 ")).toBe(100);
    for (const v of ["", "0", "101", "1.5", "-2", "zero"]) expect(rate(v)).toBe(0);
  });
});
