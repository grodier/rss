import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion, submit } from "../../shared/test/setup";
import { at, OMEGA } from "./reorder";

describe("at, the critically damped spring", () => {
  // Starts the reorder module allows: from rest, or moving at most as fast
  // towards its place as it can without overshooting (spring clamps faster
  // ones), or away from it.
  const starts = [
    [100, 0],
    [-100, 0],
    [3, 0],
    [100, -OMEGA * 100],
    [-40, OMEGA * 40],
    [100, 2000],
    [-100, -500],
  ];

  it.each(starts)("never overshoots from %d px at %d px/s", (x0, v0) => {
    for (let t = 0; t <= 2; t += 0.001) {
      const { x } = at(x0, v0, t);
      expect(Math.sign(x) === -Math.sign(x0) && Math.abs(x) > 1e-9).toBe(false);
    }
  });

  it.each(starts)("converges from %d px at %d px/s", (x0, v0) => {
    const { x, v } = at(x0, v0, 2);
    expect(Math.abs(x)).toBeLessThan(0.01);
    expect(Math.abs(v)).toBeLessThan(0.5);
  });

  it("starts where and as fast as it's told", () => {
    expect(at(80, -300, 0)).toEqual({ x: 80, v: -300 });
  });

  it("covers one row's height in about 260 ms", () => {
    expect(Math.abs(at(56, 0, 0.26).x)).toBeLessThan(0.5);
  });
});

describe("the move buttons", () => {
  isolate();

  let fetchMock: ReturnType<typeof vi.fn>;

  const row = (id: string, name: string, first: boolean, last: boolean) => `
    <li class="timeline-row" data-id="${id}" data-name="${name}">
      <span class="reorder-handle" aria-hidden="true" hidden></span>
      <form class="timeline-row-move" action="/timelines/${id}/move" method="post" data-move>
        <button type="submit" name="direction" value="up"${first ? " disabled" : ""}>Up</button>
        <button type="submit" name="direction" value="down"${last ? " disabled" : ""}>Down</button>
      </form>
    </li>`;

  beforeEach(async () => {
    document.body.innerHTML = `<ul data-reorder>${row("all", "Home", true, false)}${row("t1", "News", false, false)}${row("t2", "Tech", false, true)}</ul>`;
    fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("scrollBy", vi.fn());
    (await import("./reorder")).init();
  });

  const order = () => [...document.querySelectorAll<HTMLElement>("ul > li")].map((li) => li.dataset.id);
  const button = (id: string, direction: "up" | "down") =>
    document.querySelector<HTMLButtonElement>(`li[data-id="${id}"] button[value="${direction}"]`)!;
  const press = (id: string, direction: "up" | "down") => {
    const b = button(id, direction);
    return submit(b.form!, b);
  };
  const posted = (call: number) => {
    const [url, init] = fetchMock.mock.calls[call];
    return [
      url,
      init.method,
      init.redirect,
      [...(init.body as URLSearchParams)].map(([k, v]) => `${k}=${v}`).join("&"),
    ];
  };

  it("shows the drag handles", () => {
    expect([...document.querySelectorAll<HTMLElement>(".reorder-handle")].every((h) => !h.hidden)).toBe(true);
  });

  it("moves a row down in place and posts the whole order", async () => {
    expect(press("all", "down")).toBe(true);
    expect(order()).toEqual(["t1", "all", "t2"]);
    expect(posted(0)).toEqual(["/timelines/order", "POST", "manual", "order=t1&order=all&order=t2"]);
    await vi.waitFor(() => expect(liveRegion()).toBe("Home moved to position 2 of 3."));
    expect(document.activeElement).toBe(button("all", "down"));
  });

  it("moves a row up and updates the buttons at the ends", () => {
    press("t2", "up");
    expect(order()).toEqual(["all", "t2", "t1"]);
    press("t2", "up");
    expect(order()).toEqual(["t2", "all", "t1"]);
    expect(posted(1)[3]).toBe("order=t2&order=all&order=t1");
    expect(button("t2", "up").disabled).toBe(true);
    expect(button("all", "up").disabled).toBe(false);
    expect(button("t1", "down").disabled).toBe(true);
    // The pressed button is disabled at the end, so focus moves to its sibling.
    expect(document.activeElement).toBe(button("t2", "down"));
  });

  it("does nothing past an end", () => {
    expect(press("all", "up")).toBe(true);
    expect(order()).toEqual(["all", "t1", "t2"]);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("goes to /timelines when the order isn't saved", async () => {
    const assign = vi.fn();
    vi.stubGlobal("location", { ...location, assign });
    fetchMock.mockImplementation(async () => new Response(null, { status: 409 }));
    press("all", "down");
    await vi.waitFor(() => expect(assign).toHaveBeenCalledWith("/timelines"));
  });
});
