import { announce } from "../../shared/lib/announce";

// Reordering on /timelines (ul[data-reorder]): show each row's drag
// handle (.reorder-handle, rendered hidden), and let the move buttons
// (form[data-move]) move rows in place instead of posting. After a drop
// or a press, update the buttons' end states, announce the new position,
// and post the whole order (each row's data-id, "all" for Home) to
// /timelines/order. Anything but a 204, including the redirect for an
// order that changed in another window, goes to /timelines, where the
// flash says why; redirect: "manual" keeps that flash for it. The move
// buttons stay: they're the single-pointer and keyboard way to reorder.
// Rows glide to their places (#519; not under reduced motion): the order
// changes at once and the motion is drawn on top, so a press is never
// held up by the one before it. See "Timelines" and "Motion and
// feedback" in docs/decisions.md.

// Every row moves on a critically damped spring, x(t) = (x0 + (v0 +
// ωx0)t)e^(−ωt), from x0 px away from where it's going at v0 px/s.
// From rest it eases in and out; interrupted (another press, a drag
// changing its mind), it carries on from where it is at the speed it
// has, so nothing jumps or restarts. It never overshoots: rows don't
// bounce into each other. Rows move the real elements, not snapshots
// (a view transition would take the page's clicks while it ran), so
// the buttons keep their focus and take every press.
export const OMEGA = 28; // rad/s: one row's height in about 260 ms
const STEP = 1000 / 120; // ms between keyframes

// at is where the spring is (x px from its place) and how fast it's
// moving (v px/s) t seconds after it was x0 px away at v0 px/s.
export function at(x0: number, v0: number, t: number): { x: number; v: number } {
  const b = v0 + OMEGA * x0,
    e = Math.exp(-OMEGA * t);
  return { x: (x0 + b * t) * e, v: (b - OMEGA * (x0 + b * t)) * e };
}

// A drag in progress.
interface Drag {
  li: HTMLElement;
  all: HTMLElement[];
  index: number;
  target: number;
  tops: number[];
  heights: number[];
  gap: number;
  grab: number;
  y: number;
  t: number;
  v: number; // px/s, the pointer's
  at: number;
  frame?: number;
  clamped?: boolean;
}

export function init(): void {
  const list = document.querySelector<HTMLElement>("ul[data-reorder]");
  if (!list) return;
  const rows = () => [...list.children] as HTMLElement[];
  for (const handle of list.querySelectorAll<HTMLElement>(".reorder-handle")) handle.hidden = false;
  const reduced = matchMedia("(prefers-reduced-motion: reduce)");

  const held = new Map<HTMLElement, number>(); // row → px from its place it rests at (aside, during a drag)
  const springs = new Map<HTMLElement, { x0: number; v0: number; anim: Animation }>();
  // Where a row is drawn now, from its place in the list, and its speed.
  const motion = (row: HTMLElement) => {
    const to = held.get(row) ?? 0;
    const s = springs.get(row);
    if (!s || s.anim.playState !== "running") return { x: to, v: 0 };
    const { x, v } = at(s.x0, s.v0, ((s.anim.currentTime as number | null) ?? 0) / 1000);
    return { x: to + x, v };
  };
  // Move a row to rest `to` px from its place, from `from` px at v0 px/s.
  const spring = (row: HTMLElement, to: number, from: number, v0 = 0, zIndex: string | number = "auto") => {
    springs.get(row)?.anim.cancel();
    springs.delete(row);
    if (to) held.set(row, to);
    else held.delete(row);
    row.style.transform = to ? `translateY(${to}px)` : "";
    const x0 = from - to;
    // Faster than this towards its place and it would overshoot.
    if (x0 * v0 < 0 && Math.abs(v0) > OMEGA * Math.abs(x0)) v0 = -OMEGA * x0;
    if (reduced.matches || (Math.abs(x0) < 0.5 && Math.abs(v0) < 30)) return;
    const frames: Keyframe[] = [];
    for (let ms = 0; ; ms += STEP) {
      const { x, v } = at(x0, v0, ms / 1000);
      frames.push({ transform: `translateY(${to + x}px)`, zIndex });
      if ((Math.abs(x) < 0.5 && Math.abs(v) < 30) || ms > 1000) break;
    }
    frames.push({ transform: to ? `translateY(${to}px)` : "none", zIndex });
    const anim = row.animate(frames, { duration: (frames.length - 1) * STEP });
    // Start now, as of this frame, not at the next one: x0 and v0 are
    // where the row is in this frame, and a pending start would hold it
    // there for a frame, a stop in the middle of a move.
    anim.startTime = document.timeline.currentTime;
    springs.set(row, { x0, v0, anim });
    anim.onfinish = () => {
      if (springs.get(row)?.anim === anim) springs.delete(row);
    };
  };
  // Change the order and glide every row from where it's drawn to its
  // new place, keeping its speed. `lead` (the row the reader moved) is
  // drawn above the rows it passes, at `speed` if given.
  const glide = (change: () => void, lead: HTMLElement, z: number, speed?: number) => {
    const before = new Map(rows().map((li) => [li, { top: li.getBoundingClientRect().top, v: motion(li).v }]));
    if (speed !== undefined) before.get(lead)!.v = speed;
    for (const s of springs.values()) s.anim.cancel();
    springs.clear();
    change();
    for (const li of rows()) {
      const { top, v } = before.get(li)!;
      spring(li, 0, top - li.getBoundingClientRect().top, v, li === lead ? z : "auto");
    }
  };

  // Where the list can be seen: below the sticky bars at the top, above
  // the fixed tab bar at the bottom (only those across the list count,
  // so not the desktop rail beside it).
  const visible = () => {
    const l = list.getBoundingClientRect();
    let top = 0,
      bottom = innerHeight;
    for (const el of document.querySelectorAll(".rail, .page-head, .rail-links")) {
      if (!/^(sticky|fixed)$/.test(getComputedStyle(el).position)) continue;
      const r = el.getBoundingClientRect();
      if (r.right <= l.left || r.left >= l.right || r.height > innerHeight / 2) continue;
      if (r.top <= 1) top = Math.max(top, r.bottom);
      else if (r.bottom >= innerHeight - 1) bottom = Math.min(bottom, r.top);
    }
    return { top, bottom };
  };
  // Keep a moved row in sight, clear of the bars, scrolling with it.
  // Called while nothing is animating, so the row's box is its place.
  const reveal = (li: HTMLElement) => {
    const { top, bottom } = visible();
    const r = li.getBoundingClientRect();
    const MARGIN = 12;
    const by =
      r.bottom + MARGIN > bottom ? r.bottom + MARGIN - bottom : r.top - MARGIN < top ? r.top - MARGIN - top : 0;
    if (by) scrollBy({ top: by, behavior: reduced.matches ? "instant" : "smooth" });
  };

  const refresh = () => {
    const all = rows();
    all.forEach((li, i) => {
      const up = li.querySelector<HTMLButtonElement>('button[value="up"]');
      const down = li.querySelector<HTMLButtonElement>('button[value="down"]');
      if (up) up.disabled = i === 0;
      if (down) down.disabled = i === all.length - 1;
    });
  };
  const save = async () => {
    try {
      const res = await fetch("/timelines/order", {
        method: "POST",
        body: new URLSearchParams(rows().map((li) => ["order", li.dataset.id!])),
        redirect: "manual",
      });
      if (res.status !== 204) throw new Error(`status ${res.status}`);
    } catch {
      location.assign("/timelines");
    }
  };
  const moved = (li: HTMLElement) => {
    refresh();
    const all = rows();
    announce(`${li.dataset.name} moved to position ${all.indexOf(li) + 1} of ${all.length}.`);
    li.classList.remove("just-moved");
    void li.offsetWidth; // restart the tint
    li.classList.add("just-moved");
    save();
  };

  // Move buttons: one place up or down, keeping focus on the pressed
  // button, or its sibling once the pressed one is disabled at an end,
  // and the row in sight. The row and its neighbour trade places, the
  // moved row drawn above.
  let drag: Drag | null = null;
  list.addEventListener("submit", (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("form[data-move]")) return;
    e.preventDefault();
    const button = e.submitter as HTMLButtonElement | null;
    const li = form.closest("li")!;
    const up = button?.value === "up";
    const neighbour = up ? li.previousElementSibling : li.nextElementSibling;
    if (!button || !neighbour || drag) return;
    glide(
      () => {
        if (up) list.insertBefore(li, neighbour);
        else list.insertBefore(neighbour, li);
        reveal(li);
      },
      li,
      1,
    );
    moved(li);
    (button.disabled ? li.querySelector<HTMLButtonElement>(`button[value="${up ? "down" : "up"}"]`)! : button).focus({
      preventScroll: true,
    });
  });

  // Dragging a handle: the row lifts and follows the pointer, held
  // within the list, and the rows it passes slide out of its way as its
  // edge crosses their midpoints. The order only changes on the drop,
  // when the row settles into the gap, carrying the pointer's speed.
  // Near the top or bottom of the visible page the page scrolls, faster
  // the closer the pointer is to the edge. Escape, a cancelled pointer,
  // or leaving the window glides every row back.
  const clamp = (n: number, lo: number, hi: number) => Math.min(Math.max(n, lo), hi);

  // Place the dragged row under the pointer and move the others aside.
  // Positions are in page coordinates, from the rows' places when the
  // drag began, so scrolling during the drag moves the row with the
  // pointer.
  const follow = (drag: Drag) => {
    const { li, all, index, tops, heights, gap } = drag;
    const last = all.length - 1;
    const want = drag.y + scrollY - drag.grab;
    const dy = clamp(want, tops[0] - tops[index], tops[last] + heights[last] - tops[index] - heights[index]);
    drag.clamped = dy !== want;
    li.style.transform = `translateY(${dy}px)`;
    const top = tops[index] + dy,
      bottom = top + heights[index];
    const shift = heights[index] + gap;
    let target = index;
    all.forEach((row, i) => {
      if (i === index) return;
      const middle = tops[i] + heights[i] / 2;
      let d = 0;
      if (i < index && top < middle) {
        d = shift;
        target--;
      } else if (i > index && bottom > middle) {
        d = -shift;
        target++;
      }
      if (d !== (held.get(row) ?? 0)) {
        const { x, v } = motion(row);
        spring(row, d, x, v);
      }
    });
    drag.target = target;
  };

  const EDGE = 72; // px: how close to an edge the page starts to scroll
  const SPEED = 900; // px/s at the edge itself
  const scroll = (now: number) => {
    if (!drag) return;
    const { top, bottom } = visible();
    const l = list.getBoundingClientRect();
    // Only towards rows that are out of sight.
    const over =
      (l.top < top ? Math.max(top + EDGE - drag.y, 0) : 0) -
      (l.bottom > bottom ? Math.max(drag.y - (bottom - EDGE), 0) : 0);
    const dt = drag.at ? Math.min(now - drag.at, 50) : 0;
    drag.at = now;
    if (over && dt) {
      const v = SPEED * Math.min(Math.abs(over) / EDGE, 1) ** 2;
      scrollBy(0, (-Math.sign(over) * v * dt) / 1000);
    }
    drag.frame = requestAnimationFrame(scroll);
  };

  list.addEventListener("pointerdown", (e) => {
    const handle = e.target instanceof Element && e.target.closest<HTMLElement>(".reorder-handle");
    if (!handle || e.button !== 0 || !e.isPrimary || drag) return;
    e.preventDefault();
    const li = handle.closest("li")!;
    const all = rows();
    const index = all.indexOf(li);
    // Rows still gliding keep going; their places are where they're
    // going, and the grabbed row stops where it's drawn.
    const offsets = all.map((row) => motion(row).x);
    const rects = all.map((row) => row.getBoundingClientRect());
    const tops = rects.map((r, i) => r.top - offsets[i] + scrollY);
    const heights = rects.map((r) => r.height);
    spring(li, offsets[index], offsets[index]);
    held.delete(li);
    drag = {
      li,
      all,
      index,
      target: index,
      tops,
      heights,
      gap: all.length > 1 ? tops[1] - tops[0] - heights[0] : 0,
      grab: e.clientY + scrollY - offsets[index],
      y: e.clientY,
      t: e.timeStamp,
      v: 0, // px/s, the pointer's
      at: 0,
    };
    li.classList.add("dragging");
    handle.setPointerCapture(e.pointerId);
    drag.frame = requestAnimationFrame(scroll);
  });
  list.addEventListener("pointermove", (e) => {
    if (!drag || !e.isPrimary) return;
    const dt = e.timeStamp - drag.t;
    if (dt > 0) drag.v = 0.6 * (((e.clientY - drag.y) * 1000) / dt) + 0.4 * drag.v;
    drag.y = e.clientY;
    drag.t = e.timeStamp;
    follow(drag);
  });
  addEventListener(
    "scroll",
    () => {
      if (drag) follow(drag);
    },
    { passive: true },
  );

  const end = (cancel: boolean, e?: PointerEvent) => {
    if (!drag) return;
    const { li, all, index, frame } = drag;
    const target = cancel ? index : drag.target;
    // A pointer that had stopped, or a row held at an end, is at rest.
    const still = cancel || drag.clamped || !e || e.timeStamp - drag.t > 60;
    const speed = still ? 0 : drag.v;
    drag = null;
    cancelAnimationFrame(frame!);
    glide(
      () => {
        li.classList.remove("dragging");
        for (const row of all) row.style.transform = "";
        held.clear();
        if (target === index) return;
        // Only the dragged row moves in the document, and it keeps focus
        // if it had it (a move button pressed before the drag).
        const focused = li.contains(document.activeElement) && (document.activeElement as HTMLElement | null);
        list.insertBefore(li, all.filter((row) => row !== li)[target] ?? null);
        if (focused) focused.focus({ preventScroll: true });
      },
      li,
      2,
      speed,
    );
    if (target !== index) moved(li);
  };
  list.addEventListener("pointerup", (e) => {
    if (e.isPrimary) end(false, e);
  });
  list.addEventListener("pointercancel", () => end(true));
  list.addEventListener("lostpointercapture", () => end(true));
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && drag) {
      e.preventDefault();
      end(true);
    }
  });
  addEventListener("blur", () => end(true));
}
