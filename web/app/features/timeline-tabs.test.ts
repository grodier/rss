import { describe, expect, it } from "vitest";
import { LEAD, MAX_OVERSHOOT, REST, spring, TRAIL } from "./timeline-tabs";

// The underline's move itself needs view transitions and the Web Animations
// API, which happy-dom doesn't have, so it's checked by hand; its springs are
// tested here. The times are those "Tab to tab" in docs/decisions.md records.
const NEIGHBOUR = 60; // px: the distance to the next tab
const ACROSS = 500; // px: to a tab across the row

// error returns how far the spring is from its place, in px, every half
// millisecond until `until` ms.
const errors = (s: ReturnType<typeof spring>, distance: number, until: number) => {
  const out: [number, number][] = [];
  for (let ms = 0; ms <= until; ms += 0.5) out.push([ms, (s.at(ms) - 1) * distance]);
  return out;
};

// inPlace returns when the spring is last 1 px or more from its place.
const inPlace = (s: ReturnType<typeof spring>, distance: number) =>
  Math.max(
    ...errors(s, distance, 1000)
      .filter(([, e]) => Math.abs(e) >= 1)
      .map(([ms]) => ms),
  );

describe("spring", () => {
  const distances = [1, 20, NEIGHBOUR, 120, 300, ACROSS, 1000];

  it.each(distances)("starts at 0, at rest, and ends at the target over %d px", (d) => {
    for (const params of [LEAD, TRAIL]) {
      const s = spring(params, d);
      expect(s.at(0)).toBe(0);
      expect(s.at(0.01)).toBeCloseTo(0, 4);
      // From settle on, it stays within REST px of its place.
      for (const [ms, e] of errors(s, d, s.settle + 1000)) if (ms >= s.settle) expect(Math.abs(e)).toBeLessThan(REST);
    }
  });

  it.each(distances)("overshoots by at most MAX_OVERSHOOT px over %d px", (d) => {
    for (const params of [LEAD, TRAIL]) {
      const over = Math.max(...errors(spring(params, d), d, 1000).map(([, e]) => e));
      expect(over).toBeLessThanOrEqual(MAX_OVERSHOOT + 1e-9);
    }
    expect(MAX_OVERSHOOT).toBe(6);
  });

  it("overshoots its share of a short distance", () => {
    const d = 60;
    const over = Math.max(...errors(spring(LEAD, d), d, 1000).map(([, e]) => e));
    expect(over).toBeCloseTo(LEAD.overshoot * d, 1);
  });

  it("reaches a neighbour in about 175 ms", () => {
    expect(inPlace(spring(LEAD, NEIGHBOUR), NEIGHBOUR)).toBeGreaterThan(150);
    expect(inPlace(spring(LEAD, NEIGHBOUR), NEIGHBOUR)).toBeLessThan(200);
    expect(inPlace(spring(TRAIL, NEIGHBOUR), NEIGHBOUR)).toBeGreaterThan(150);
    expect(inPlace(spring(TRAIL, NEIGHBOUR), NEIGHBOUR)).toBeLessThan(200);
  });

  it("reaches a tab across the row in about 240 ms", () => {
    expect(inPlace(spring(LEAD, ACROSS), ACROSS)).toBeGreaterThan(215);
    expect(inPlace(spring(LEAD, ACROSS), ACROSS)).toBeLessThan(265);
  });

  it("is settled by 200–320 ms", () => {
    for (const s of [spring(LEAD, NEIGHBOUR), spring(TRAIL, NEIGHBOUR), spring(LEAD, ACROSS)]) {
      expect(s.settle).toBeGreaterThan(200);
      expect(s.settle).toBeLessThan(320);
    }
  });
});
